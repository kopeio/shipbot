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
	"bytes"
	"flag"
	"fmt"
	"github.com/golang/glog"
	"github.com/google/go-github/github"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"
)

var (
	tag = "v1.4.4"

	credentialsFile = path.Join(os.Getenv("HOME"), ".shipbot/github_token")
	builddir        = path.Join(os.Getenv("HOME"), "release/src/k8s.io/kops")
)

type AssetMapping struct {
	Source     string
	GithubName string
}

var assetMappings = []*AssetMapping{
	{
		Source:     path.Join(builddir, ".build/dist/darwin/amd64/kops"),
		GithubName: "kops-darwin-amd64",
	},
	{
		Source:     path.Join(builddir, ".build/dist/linux/amd64/kops"),
		GithubName: "kops-linux-amd64",
	},
}

func main() {
	flag.Set("logtostderr", "true")
	flag.Parse()

	shipbot := &Shipbot{
		Owner: "kubernetes",
		Repo:  "kops",
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

	err := shipbot.DoRelease()
	if err != nil {
		glog.Fatalf("unexpected error: %v", err)
	}
}

type Shipbot struct {
	Client *github.Client
	Owner  string
	Repo   string
}

func (sb *Shipbot) DoRelease() error {
	glog.Infof("listing github releases for %s/%s", sb.Owner, sb.Repo)
	releases, _, err := sb.Client.Repositories.ListReleases(sb.Owner, sb.Repo, &github.ListOptions{})
	if err != nil {
		return fmt.Errorf("error listing releases: %v", err)
	}

	var found *github.RepositoryRelease
	for _, release := range releases {
		if sv(release.TagName) == tag {
			glog.Infof("found release: %v", sv(release.TagName))
			found = release
		}
	}

	if found == nil {
		commitSha, err := findCommitSha(builddir, tag)
		if err != nil {
			return fmt.Errorf("cannot find sha for tag %q: %v", tag, err)
		}
		glog.Infof("SHA is %q", commitSha)
		release := &github.RepositoryRelease{
			TagName:         s(tag),
			TargetCommitish: s(commitSha),
			Name:            s(tag),
			Body:            s("Release " + tag + " (draft)"),
			Draft:           b(true),
		}

		glog.Infof("creating github release for %s/%s/%s", sb.Owner, sb.Repo, tag)
		found, _, err = sb.Client.Repositories.CreateRelease(sb.Owner, sb.Repo, release)
		if err != nil {
			return fmt.Errorf("error creating release: %v", err)
		}
	}

	glog.Infof("listing github release assets for %s/%s/%s", sb.Owner, sb.Repo, tag)
	assets, _, err := sb.Client.Repositories.ListReleaseAssets(sb.Owner, sb.Repo, iv(found.ID), &github.ListOptions{})
	if err != nil {
		return fmt.Errorf("error listing assets: %v", err)
	}

	assetMap := make(map[string]*github.ReleaseAsset)
	for _, asset := range assets {
		assetMap[sv(asset.Name)] = asset
	}

	for _, assetMapping := range assetMappings {
		err := sb.syncAsset(found, assetMapping, assetMap)
		if err != nil {
			return err
		}
	}

	return nil
}

func findCommitSha(basedir string, tag string) (string, error) {
	cmd := exec.Command("git", "rev-list", "-n", "1", tag)
	cmd.Dir = basedir
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("error getting git sha @%q: %v", tag, err)
	}
	sha := strings.TrimSpace(out.String())
	if len(sha) != 40 {
		return "", fmt.Errorf("git sha had unexpected length: %q", sha)
	}
	return sha, nil
}

func (sb *Shipbot) syncAsset(release *github.RepositoryRelease, assetMapping *AssetMapping, assets map[string]*github.ReleaseAsset) error {
	srcStat, err := os.Stat(assetMapping.Source)
	if err != nil {
		return fmt.Errorf("error doing stat %q: %v", assetMapping.Source, err)
	}

	existing := assets[assetMapping.GithubName]
	if existing != nil {
		if int64(iv(existing.Size)) != srcStat.Size() {
			// TODO: Support force-replace mode?
			return fmt.Errorf("asset %q size did not match", assetMapping.GithubName)
		} else {
			glog.Infof("asset sizes match; assuming the same for %s", assetMapping.GithubName)
			return nil
		}
	}

	f, err := os.Open(assetMapping.Source)
	if err != nil {
		return fmt.Errorf("error opening %q: %v", assetMapping.Source, err)
	}
	defer f.Close()

	uploadOptions := &github.UploadOptions{
		Name: assetMapping.GithubName,
	}

	glog.Infof("creating github release assets for %s/%s/%s %q", sb.Owner, sb.Repo, tag, assetMapping.GithubName)
	asset, _, err := sb.Client.Repositories.UploadReleaseAsset(sb.Owner, sb.Repo, iv(release.ID), uploadOptions, f)
	if err != nil {
		return fmt.Errorf("error uploading assets %q: %v", assetMapping.GithubName, err)
	}

	glog.Infof("uploaded asset: %v", asset)
	return nil
}

func sv(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func iv(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func s(v string) *string {
	return &v
}

func b(v bool) *bool {
	return &v
}
