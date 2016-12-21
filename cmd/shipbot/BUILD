package(default_visibility = ["//visibility:public"])

licenses(["notice"])

load(
    "@io_bazel_rules_go//go:def.bzl",
    "go_binary",
)

go_binary(
    name = "shipbot",
    srcs = [
        "main.go",
    ],
    deps = [
        "@com_github_golang_glog//:go_default_library",
        "@com_github_google_go_github//github:go_default_library",
        #"@org_golang_x_oauth2//:go_default_library",
    ],
)
