package main

// 多用户隔离：任务、仓库、还原记录都记下创建者（飞牛用户 UID）。
//
// 规则：
//   - 管理员能看到、操作全部；没有身份（Docker / 裸端口部署）视同管理员，行为与旧版一致；
//   - 普通用户只能看到、操作自己创建的；
//   - Owner=0 是 0.5.2 之前创建的旧数据，归管理员；
//   - 走 OpenList 中转的内置云盘（天翼 / 115）是管理员配置的共享账号，普通用户不能用，
//     只能用自己添加的 WebDAV 连接。
//
// 不可访问的资源一律按「不存在」返回 404，不暴露别人有什么。

import (
	"net/http"
	"strings"
)

// restricted：这个请求是否受隔离限制（有身份且不是管理员）
func restricted(id identity) bool { return id.OK && !id.Admin }

func canAccess(id identity, owner int) bool {
	if !restricted(id) {
		return true
	}
	return owner != 0 && owner == id.UID
}

// ownerFor 新建资源时记录的创建者
func ownerFor(id identity) int {
	if id.OK {
		return id.UID
	}
	return 0
}

func notFound(w http.ResponseWriter, what string) {
	writeJSON(w, 404, map[string]string{"error": what + "不存在"})
}

// pathID 取 /api/<kind>/<id>/... 里的 id
func pathID(path, marker string) string {
	rest := afterMarker(path, marker)
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// guardTask 校验当前用户能否访问任务；不能时已写好 404，返回 false
func guardTask(w http.ResponseWriter, r *http.Request, id string) bool {
	ident := identityFrom(r)
	if !restricted(ident) {
		return true
	}
	cfgMu.Lock()
	t := findTask(id)
	ok := t != nil && canAccess(ident, t.Owner)
	cfgMu.Unlock()
	if !ok {
		notFound(w, "任务")
	}
	return ok
}

func guardRepo(w http.ResponseWriter, r *http.Request, id string) bool {
	ident := identityFrom(r)
	if !restricted(ident) {
		return true
	}
	cfgMu.Lock()
	rp := findRepo(id)
	ok := rp != nil && canAccess(ident, rp.Owner)
	cfgMu.Unlock()
	if !ok {
		notFound(w, "仓库")
	}
	return ok
}

// canUseRemoteLocked：普通用户只能用自己仓库的 WebDAV 远端；OpenList 中转云盘仅管理员。调用方持有 cfgMu
func canUseRemoteLocked(id identity, cloud, remote string) bool {
	if !restricted(id) {
		return true
	}
	if cloud != "" || strings.HasPrefix(remote, "ol:") || remote == "ol" {
		return false
	}
	if _, isOL := cloudLabelMap[remote]; isOL {
		return false
	}
	for _, rp := range cfg.Repos {
		if rp.Remote == remote && canAccess(id, rp.Owner) {
			return true
		}
	}
	return false
}

// taskOwnerLocked 还原记录、历史记录按所属任务判定；任务已删除时用记录里存的 Owner
func taskOwnerLocked(taskID string, fallback int) int {
	if t := findTask(taskID); t != nil {
		return t.Owner
	}
	return fallback
}
