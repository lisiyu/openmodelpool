//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type AdminUser struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	Email        string    `json:"email"`
	CreatedAt    time.Time `json:"created_at"`
}

type AdminFile struct {
	Admin AdminUser `json:"admin"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("用法: go run cmd_resetpwd.go <新密码>")
		fmt.Println("示例: go run cmd_resetpwd.go MyNewPass123")
		fmt.Println("请在 openmodelpool 部署目录下执行（含 data/ 子目录的那个）")
		os.Exit(1)
	}

	newPwd := os.Args[1]

	// Try CWD first, then fallback to /root/openmodelpool-deploy
	dataDir := filepath.Join(".", "data")
	if _, err := os.Stat(filepath.Join(dataDir, "admin.json")); err != nil {
		dataDir = "/root/openmodelpool-deploy/data"
	}
	adminFile := filepath.Join(dataDir, "admin.json")

	data, err := os.ReadFile(adminFile)
	if err != nil {
		fmt.Printf("读取 admin.json 失败: %v\n", err)
		os.Exit(1)
	}

	var af AdminFile
	if err := json.Unmarshal(data, &af); err != nil {
		fmt.Printf("解析 admin.json 失败: %v\n", err)
		os.Exit(1)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPwd), bcrypt.DefaultCost)
	if err != nil {
		fmt.Printf("生成密码 hash 失败: %v\n", err)
		os.Exit(1)
	}

	af.Admin.PasswordHash = string(hash)

	out, _ := json.MarshalIndent(af, "", "  ")
	if err := os.WriteFile(adminFile, out, 0600); err != nil {
		fmt.Printf("写入 admin.json 失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ 密码已重置\n")
	fmt.Printf("  用户名: %s\n", af.Admin.Username)
	fmt.Printf("  新密码: %s\n", newPwd)
	fmt.Printf("  配置: %s（未改动）\n", adminFile)
}
