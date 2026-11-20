package api

import (
	"net/http"
	"os"
)

// oledOffFlag 这个文件存在 = 正面小屏熄屏。pp-oled 程序每隔几秒读它决定点屏还是熄屏。
// 管家在宿主机上跑，直接 touch/rm 这个文件就能控制小屏（管理页的开关按钮走这里）。
const oledOffFlag = "/mmc/pp-data/oled-off"

// oledState 返回小屏当前是开着还是熄了。
func (s *Server) oledState(w http.ResponseWriter, _ *http.Request) {
	_, err := os.Stat(oledOffFlag)
	writeJSON(w, http.StatusOK, map[string]bool{"on": os.IsNotExist(err)})
}

// oledSet 开/关小屏。body: {"on": true} 点亮，{"on": false} 熄屏。
func (s *Server) oledSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On bool `json:"on"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	if body.On {
		// 删掉标志 = 点亮
		if err := os.Remove(oledOffFlag); err != nil && !os.IsNotExist(err) {
			writeErr(w, http.StatusInternalServerError, "点亮小屏失败："+err.Error())
			return
		}
	} else {
		// 建标志 = 熄屏。先确保目录在。
		if err := os.MkdirAll("/mmc/pp-data", 0o755); err != nil {
			writeErr(w, http.StatusInternalServerError, "熄屏失败："+err.Error())
			return
		}
		if err := os.WriteFile(oledOffFlag, []byte("off\n"), 0o644); err != nil {
			writeErr(w, http.StatusInternalServerError, "熄屏失败："+err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"on": body.On})
}
