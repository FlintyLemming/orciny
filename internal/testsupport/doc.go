// Package testsupport 提供跨组件集成测试的脚手架（spec §12.1）。
//
// 实现文件全部带 //go:build testing。本文件不带 tag，
// 保证不加 tag 时 `go build ./...` 看到的是一个合法的空包，而不是
// 「build constraints exclude all Go files」。
//
// 边界提示：本包在 hub/ 树之外，因此只能通过 hub 与 agent 的公开入口
// 做集成测试，够不到 hub/internal/*（spec §2.2）。那些包的单元测试必须
// 写在包内部。
package testsupport
