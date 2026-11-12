package main

// 数据迁移：让「落后很多个版本」的用户一步升到最新。
//
// 规则（新增迁移时务必遵守）：
//  1. 只在 migrations 末尾追加，To 严格递增；已发布的迁移永远不要删、不要改。
//  2. 迁移操作的是 config.json 的原始 JSON（map），而不是 Config 结构体——
//     结构体会随版本演进，老字段可能已经不存在，用 map 才能读到旧数据。
//  3. 每一步都要可重复执行（幂等）：中途断电重启后会从头再跑这一步。
//  4. 只改数据形状，不做网络请求、不调 restic。
//
// 执行时机：loadConfig() 里、反序列化成 Config 之前。第一次需要迁移时会把原文件
// 备份成 config.json.bak-schema<N>，迁移失败则保留原文件不动、以旧数据启动。

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

type migration struct {
	To   int
	Desc string
	Run  func(raw map[string]any) error
}

var migrations = []migration{
	{To: 1, Desc: "基线：引入 schema 字段（0.4.4 之前的配置都视为 schema 0）", Run: func(raw map[string]any) error { return nil }},
	// 示例（以后新增时照抄）：
	// {To: 2, Desc: "任务新增 xxx 字段，默认 7", Run: func(raw map[string]any) error {
	// 	for _, t := range listOf(raw, "tasks") {
	// 		if _, ok := t["xxx"]; !ok { t["xxx"] = 7 }
	// 	}
	// 	return nil
	// }},
}

// currentSchema 本版本代码认识的最高数据版本
func currentSchema() int {
	if len(migrations) == 0 {
		return 0
	}
	return migrations[len(migrations)-1].To
}

// listOf 取 raw[key] 里的对象数组，方便迁移函数逐项修改（修改直接作用在 raw 上）
func listOf(raw map[string]any, key string) []map[string]any {
	arr, _ := raw[key].([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, v := range arr {
		if m, ok := v.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func schemaOf(raw map[string]any) int {
	if f, ok := raw["schema"].(float64); ok {
		return int(f)
	}
	return 0
}

// migrateConfigBytes 把任意旧版本的 config.json 升到 currentSchema。
// 返回迁移后的字节、是否发生了变化。from > current（回滚到老程序）时原样返回。
func migrateConfigBytes(data []byte) ([]byte, bool, error) {
	raw := map[string]any{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return data, false, fmt.Errorf("config.json 解析失败：%w", err)
	}
	from := schemaOf(raw)
	if from >= currentSchema() {
		return data, false, nil
	}
	for _, m := range migrations {
		if m.To <= from {
			continue
		}
		if err := m.Run(raw); err != nil {
			return data, false, fmt.Errorf("迁移到 schema %d（%s）失败：%w", m.To, m.Desc, err)
		}
		raw["schema"] = m.To
		log.Printf("[migrate] schema %d → %d：%s", from, m.To, m.Desc)
		from = m.To
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return data, false, err
	}
	return out, true, nil
}

// migrateConfigFile 在 loadConfig 读文件前调用：需要时备份 + 迁移 + 原子写回
func migrateConfigFile() {
	p := filepath.Join(varDir(), "config.json")
	data, err := os.ReadFile(p)
	if err != nil {
		return // 首次安装没有配置文件，loadConfig 会按空配置启动
	}
	out, changed, err := migrateConfigBytes(data)
	if err != nil {
		log.Printf("[migrate] %v（保留原配置，按旧数据启动）", err)
		return
	}
	if !changed {
		return
	}
	raw := map[string]any{}
	_ = json.Unmarshal(data, &raw)
	bak := fmt.Sprintf("%s.bak-schema%d", p, schemaOf(raw))
	if _, err := os.Stat(bak); os.IsNotExist(err) {
		_ = os.WriteFile(bak, data, 0o600)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		log.Printf("[migrate] 写入失败：%v", err)
		return
	}
	if err := os.Rename(tmp, p); err != nil {
		log.Printf("[migrate] 替换失败：%v", err)
	}
}
