package(default_visibility = ["//visibility:public"])

licenses(["notice"])

load(
    "@io_bazel_rules_go//go:def.bzl",
    "go_binary",
    "go_library",
)

go_binary(
    name = "shipbot",
    library = ":go_default_library",
)

go_library(
    name = "go_default_library",
    srcs = ["main.go"],
    visibility = ["//visibility:private"],
    deps = [
        "@com_github_ghodss_yaml//:go_default_library",
        "@com_github_golang_glog//:go_default_library",
        "@com_github_google_go_github//github:go_default_library",
    ],
)
