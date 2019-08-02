/*
Copyright 2015 The Kubernetes Authors All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/golang/glog"
	"github.com/google/go-github/github"
)

var (
	credentialsFile = path.Join(os.Getenv("HOME"), ".shipbot/github_token")
	basedir         = path.Join(os.Getenv("HOME"), ".shipbot/data/")
)

type Config struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
}

func main() {
	configFile := ""
	flag.StringVar(&configFile, "config", "", "config file to use")
	format := ""
	flag.StringVar(&format, "format", format, "output format")
	flag.Set("logtostderr", "true")
	flag.Parse()

	if configFile == "" {
		glog.Fatalf("must specify -config")
	}

	configBytes, err := ioutil.ReadFile(configFile)
	if err != nil {
		glog.Fatalf("error reading config file %q: %v", configFile, err)
	}

	config := &Config{}
	if err := yaml.Unmarshal(configBytes, config); err != nil {
		glog.Fatalf("error parsing config file %q: %v", configFile, err)
	}

	shipbot := &RelnotesBuilder{
		Config:  config,
		DataDir: path.Join(basedir, config.Owner, config.Repo),
		Format:  format,
	}

	{
		credBytes, err := ioutil.ReadFile(credentialsFile)
		if err != nil {
			glog.Fatalf("error reading github token from %q: %v", credentialsFile, err)
		}
		creds := strings.TrimSpace(string(credBytes))
		tokens := strings.Split(creds, ":")
		if len(tokens) != 2 {
			glog.Fatalf("unexpected credentials format in %q", credentialsFile)
		}
		basicAuthTransport := &github.BasicAuthTransport{
			Username: tokens[0],
			Password: tokens[1],
		}

		//ts := oauth2.StaticTokenSource(
		//	&oauth2.Token{AccessToken: creds},
		//)
		//tc := oauth2.NewClient(oauth2.NoContext, ts)
		//shipbot.Client = github.NewClient(tc)
		shipbot.Client = github.NewClient(basicAuthTransport.Client())
	}

	var out bytes.Buffer
	if err := shipbot.BuildRelnotes(os.Stdin, &out); err != nil {
		glog.Fatalf("unexpected error: %v", err)
	}
	fmt.Print(out.String())
}

type RelnotesBuilder struct {
	Client *github.Client
	Config *Config

	DataDir string

	Format string
}

func (b *RelnotesBuilder) BuildRelnotes(in io.Reader, out *bytes.Buffer) error {
	var prs []int
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := scanner.Text()

		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "#")
		if line == "" {
			continue
		}

		pr, err := strconv.Atoi(line)
		if err != nil {
			glog.Fatalf("error parsing line: %s", line)
		}
		prs = append(prs, pr)
	}

	ctx := context.Background()
	for _, prNumber := range prs {
		//pr, _, err := sb.Client.PullRequests.Get(ctx, sb.Config.Owner, sb.Config.Repo, prNumber)

		pr, err := b.readPR(ctx, prNumber)
		if err != nil {
			return err
		}

		commits, err := b.readCommits(ctx, prNumber)
		if err != nil {
			return err
		}

		comments, err := b.readIssueComments(ctx, pr.GetNumber())
		if err != nil {
			return err
		}

		for _, comment := range comments {
			glog.V(2).Infof("comment: %v", comment)
		}

		//glog.Infof("PR #%d: %s", prNumber, pr.Title)
		var authors []string
		seen := make(map[string]bool)
		for _, commit := range commits {
			glog.V(2).Infof("commit %s", commit)
			author := commit.Author.GetLogin()
			if author == "" {
				continue
			}

			if seen[author] {
				continue
			}
			seen[author] = true
			authors = append(authors, fmt.Sprintf("[@%s](%s)", author, commit.Author.GetHTMLURL()))
		}

		title := pr.GetTitle()
		if strings.HasPrefix(title, "Cherry pick of #") {
			picked := strings.TrimPrefix(title, "Cherry pick of #")
			picked = strings.Split(picked, " ")[0]

			picked = strings.Trim(picked, ":")

			prNumber, err := strconv.Atoi(picked)
			if err != nil {
				return fmt.Errorf("error parsing cherry pick line %q", title)
			}

			pickedPR, err := b.readPR(ctx, prNumber)
			if err != nil {
				return err
			}
			pr = pickedPR
		}

		if b.Format == "author" {
			for _, a := range authors {
				fmt.Fprintf(out, "%s\n", a)
			}
		} else {
			fmt.Fprintf(out, "* %s %s [#%d](%s)\n", pr.GetTitle(), strings.Join(authors, ","), pr.GetNumber(), pr.GetHTMLURL())
		}
	}

	return nil
}

func (b *RelnotesBuilder) readPR(ctx context.Context, prNumber int) (*github.PullRequest, error) {
	p := filepath.Join(b.DataDir, "repos", b.Config.Owner, b.Config.Repo, "pulls", strconv.Itoa(prNumber), "data.json")

	var data []byte
	{
		b, err := ioutil.ReadFile(p)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("error reading cache file %q: %v", p, err)
			}
		} else {
			glog.V(2).Infof("Using cached file for %s", p)
			data = b
		}
	}

	if data == nil {
		u := fmt.Sprintf("repos/%v/%v/pulls/%d", b.Config.Owner, b.Config.Repo, prNumber)
		req, err := b.Client.NewRequest("GET", u, nil)
		if err != nil {
			return nil, fmt.Errorf("error building request for %s: %v", u, err)
		}

		var buf bytes.Buffer
		if _, err := b.Client.Do(ctx, req, &buf); err != nil {
			return nil, fmt.Errorf("error requesting PR for %s: %v", u, err)
		}

		data = buf.Bytes()
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return nil, fmt.Errorf("error creating directories for %q: %v", p, err)
		}
		if err := ioutil.WriteFile(p, data, 0644); err != nil {
			return nil, fmt.Errorf("error writing cache file %q: %v", p, err)
		}
		glog.V(2).Infof("Wrote cached file for %s", p)
	}

	pr := new(github.PullRequest)

	if err := json.NewDecoder(bytes.NewReader(data)).Decode(pr); err != nil {
		return nil, fmt.Errorf("error parsing PR for %s: %v", p, err)
	}

	return pr, nil

}

func (b *RelnotesBuilder) readCommits(ctx context.Context, prNumber int) ([]*github.RepositoryCommit, error) {
	p := filepath.Join(b.DataDir, "repos", b.Config.Owner, b.Config.Repo, "pulls", strconv.Itoa(prNumber), "commits.json")

	var data []byte
	{
		b, err := ioutil.ReadFile(p)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("error reading cache file %q: %v", p, err)
			}
		} else {
			glog.V(2).Infof("Using cached file for %s", p)
			data = b
		}
	}

	if data == nil {
		var commits []*github.RepositoryCommit

		//sb.Client.PullRequests.ListCommits()...
		page := 1
		perPage := 50
		for {
			u := fmt.Sprintf("repos/%v/%v/pulls/%d/commits?page=%d&per_page=%d", b.Config.Owner, b.Config.Repo, prNumber, page, perPage)

			req, err := b.Client.NewRequest("GET", u, nil)
			if err != nil {
				return nil, fmt.Errorf("error requesting commits for %s: %v", u, err)
			}

			var buf bytes.Buffer
			resp, err := b.Client.Do(ctx, req, &buf)
			if err != nil {
				return nil, fmt.Errorf("error requesting commits for %s: %v", u, err)
			}

			var thisPage []*github.RepositoryCommit
			if err := json.NewDecoder(&buf).Decode(&thisPage); err != nil {
				return nil, fmt.Errorf("error parsing commits for %s: %v", u, err)
			}
			commits = append(commits, thisPage...)

			page = resp.NextPage
			if page == 0 {
				break
			}
		}

		jsonBytes, err := json.MarshalIndent(commits, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("error serializing commits for %s: %v", p, err)
		}
		data = jsonBytes
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return nil, fmt.Errorf("error creating directories for %q: %v", p, err)
		}
		if err := ioutil.WriteFile(p, data, 0644); err != nil {
			return nil, fmt.Errorf("error writing cache file %q: %v", p, err)
		}
		glog.V(2).Infof("Wrote cached file for %s", p)
	}

	{
		var commits []*github.RepositoryCommit

		if err := json.NewDecoder(bytes.NewReader(data)).Decode(&commits); err != nil {
			return nil, fmt.Errorf("error parsing commits for %s: %v", p, err)
		}

		return commits, nil
	}

}

func (b *RelnotesBuilder) readIssueComments(ctx context.Context, issueNumber int) ([]*github.IssueComment, error) {
	p := filepath.Join(b.DataDir, "repos", b.Config.Owner, b.Config.Repo, "issues", strconv.Itoa(issueNumber), "comments.json")

	var data []byte
	{
		b, err := ioutil.ReadFile(p)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("error reading cache file %q: %v", p, err)
			}
		} else {
			glog.V(2).Infof("Using cached file for %s", p)
			data = b
		}
	}

	if data == nil {
		var all []*github.IssueComment

		//sb.Client.PullRequests.ListCommits()...
		page := 1
		perPage := 50
		for {
			u := fmt.Sprintf("repos/%v/%v/issues/%d/comments?page=%d&per_page=%d", b.Config.Owner, b.Config.Repo, issueNumber, page, perPage)

			req, err := b.Client.NewRequest("GET", u, nil)
			if err != nil {
				return nil, fmt.Errorf("error requesting issue comments for %s: %v", u, err)
			}

			var buf bytes.Buffer
			resp, err := b.Client.Do(ctx, req, &buf)
			if err != nil {
				return nil, fmt.Errorf("error requesting issue comments for %s: %v", u, err)
			}

			var thisPage []*github.IssueComment
			if err := json.NewDecoder(&buf).Decode(&thisPage); err != nil {
				return nil, fmt.Errorf("error parsing issue comments for %s: %v", u, err)
			}
			all = append(all, thisPage...)

			page = resp.NextPage
			if page == 0 {
				break
			}
		}

		jsonBytes, err := json.MarshalIndent(all, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("error serializing issue comments for %s: %v", p, err)
		}
		data = jsonBytes
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return nil, fmt.Errorf("error creating directories for %q: %v", p, err)
		}
		if err := ioutil.WriteFile(p, data, 0644); err != nil {
			return nil, fmt.Errorf("error writing cache file %q: %v", p, err)
		}
		glog.V(2).Infof("Wrote cached file for %s", p)
	}

	{
		var comments []*github.IssueComment

		if err := json.NewDecoder(bytes.NewReader(data)).Decode(&comments); err != nil {
			return nil, fmt.Errorf("error parsing issue comments for %s: %v", p, err)
		}

		return comments, nil
	}

}
