
git log ..master --oneline | grep Merge.pull.requ | cut -f 5 -d ' '

git log origin/release-1.6.. --oneline | grep Merge.pull.requ | cut -d ' ' -f 5 > /tmp/prs


cat /tmp/prs | bazel-bin/cmd/relnotes/relnotes -config ~/k8s/src/k8s.io/kops/.shipbot.yaml 
