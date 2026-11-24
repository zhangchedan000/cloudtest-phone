// Package assets 打包机型档案（profiles/）、管理页（web/dist）和导入机型包用的脚本（tools/）。
// 这几个目录由 build.sh 编译前从仓库复制过来，不提交到 git。
package assets

import "embed"

//go:embed all:profiles all:web all:tools
var FS embed.FS
