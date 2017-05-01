shipbot:
	#go install kope.io/shipbot/cmd/...
	bazel build //cmd/...
	echo "Output in bazel-bin/cmd/shipbot/shipbot"

gofmt:
	gofmt -w -s .

