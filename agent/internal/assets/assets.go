// Package assets 打包机型档案（profiles/）和管理页（web/dist）。
// 这两个目录由 build.sh 编译前从仓库复制过来，不提交到 git。
package assets

import "embed"

//go:embed all:profiles all:web
var FS embed.FS
