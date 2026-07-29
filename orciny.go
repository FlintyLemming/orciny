// Package orciny 只承载 hub 与 agent 共同引用的版本常量。
// 这里不允许放任何逻辑（spec §2.2 第 3 条）。
package orciny

import "github.com/blang/semver/v4"

// Version 是 hub 与 agent 的共同版本号。
// 声明为 var 而非 const，供 goreleaser 用 -ldflags -X 注入构建版本。
var Version = "0.1.0"

// AppName 用于二进制名、目录名与镜像名，全仓库唯一来源。
const AppName = "orciny"

// MinAgentVersion 是 hub 接受的最低 agent 版本。
// 低于此版本的 agent 在握手第一步就被 CodeVersionTooOld 拒绝（spec §3.4）。
var MinAgentVersion = semver.MustParse("0.1.0")
