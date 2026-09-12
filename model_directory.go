package main

import (
	"net/http"
	"sort"
	"strings"
	"time"
)

// 公开只读模型目录（Phase 4 "开放评测基准"的基础数据面）。
//
// 教育科研对象拿到的是一份**无需鉴权**的"本网关现在提供哪些免费/社区共享
// 模型"快照，可直接用作可复现评测的模型枚举来源。
//
// 隐私红线：目录**永不包含**本机私有（非 free- 前缀）provider 模型——
// 只暴露社区公共面（免费池 + 联邦信任池主动共享的模型）。任何 key 类型在
// /v1/models 里都看不到的模型，这里更不会出现。

type ModelDirectoryEntry struct {
	ID       string   `json:"id"`
	Sources  []string `json:"sources"` // "free-pool" 或共享这些模型的联邦 peer 名
	FreePool bool     `json:"free_pool"`
}

type ModelDirectorySnapshot struct {
	SnapshotAt    string                `json:"snapshot_at"`
	TotalModels   int                   `json:"total_models"`
	FreePoolCount int                   `json:"free_pool_count"`
	MeshSources   []string              `json:"mesh_sources"`
	Models        []ModelDirectoryEntry `json:"models"`
}

func collectModelDirectory() ModelDirectorySnapshot {
	modelSrc := make(map[string]map[string]bool)

	// 1. Free pool: enabled, public, anonymous-opaque free-* providers only.
	if pm != nil {
		for _, p := range pm.EnabledRaw() {
			if !strings.HasPrefix(p.ID, "free-") {
				continue
			}
			for _, m := range p.Models {
				if modelSrc[m.ID] == nil {
					modelSrc[m.ID] = make(map[string]bool)
				}
				modelSrc[m.ID]["free-pool"] = true
			}
		}
	}

	// 2. Federated sharing: trust-pool peers advertising models (same source
	//    of truth as the /v1/models federation aggregation).
	if fed != nil && fed.IsEnabled() {
		pool := fed.GetTrustPool()
		selfID := ""
		if node != nil {
			selfID = node.NodeID()
		}
		for i := range pool.Nodes {
			n := pool.Nodes[i]
			if n.NodeID == selfID {
				continue
			}
			var name string
			if n.GitHubUser != "" {
				name = n.GitHubUser
			} else if n.Endpoint != "" {
				name = n.Endpoint
			} else {
				name = n.NodeID
			}
			for _, sp := range n.SharedProviders {
				for _, m := range sp.Models {
					if modelSrc[m] == nil {
						modelSrc[m] = make(map[string]bool)
					}
					modelSrc[m][name] = true
				}
			}
			for _, m := range n.SharedModels {
				if modelSrc[m] == nil {
					modelSrc[m] = make(map[string]bool)
				}
				modelSrc[m][name] = true
			}
		}
	}

	snapshot := ModelDirectorySnapshot{
		SnapshotAt: time.Now().Format(time.RFC3339),
		Models:     make([]ModelDirectoryEntry, 0, len(modelSrc)),
	}
	meshNames := make(map[string]bool)
	for id, srcs := range modelSrc {
		entry := ModelDirectoryEntry{ID: id, FreePool: srcs["free-pool"]}
		for s := range srcs {
			entry.Sources = append(entry.Sources, s)
			if s != "free-pool" {
				meshNames[s] = true
			}
		}
		sort.Strings(entry.Sources)
		snapshot.Models = append(snapshot.Models, entry)
		if entry.FreePool {
			snapshot.FreePoolCount++
		}
	}
	sort.Slice(snapshot.Models, func(i, j int) bool { return snapshot.Models[i].ID < snapshot.Models[j].ID })
	for s := range meshNames {
		snapshot.MeshSources = append(snapshot.MeshSources, s)
	}
	sort.Strings(snapshot.MeshSources)
	snapshot.TotalModels = len(snapshot.Models)
	return snapshot
}

func handleModelDirectory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, collectModelDirectory())
}
