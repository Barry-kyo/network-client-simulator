package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// ANSIエスケープシーケンス：コンソール出力を色付けして視認性を高めます。
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorCyan   = "\033[36m"
)

// logRequest は、受信したリクエストの送信元IP、パス、メソッド、User-Agent、処理結果を色付きでログ出力します。
func logRequest(r *http.Request, status int, msg string) {
	// リモートアドレスからIP部分を抽出（ポート番号を除去）
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	ua := r.UserAgent()
	if ua == "" {
		ua = "(none)"
	}

	// ステータスコードに応じて色を選択 (正常系は緑、警告系は黄、エラー系は赤)
	color := ColorGreen
	if status >= 400 {
		color = ColorRed
	} else if status >= 300 {
		color = ColorYellow
	}

	log.Printf("%s[%-4s] %s -> %s %s | UA: %s | Result: %s%s\n",
		color, r.Method, ip, r.URL.Path, r.Proto, ua, msg, ColorReset)
}

func main() {
	log.Println("Starting mock server on :8080...")

	// 1. ホームエンドポイント (GET /)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			w.WriteHeader(http.StatusNotFound)
			logRequest(r, http.StatusNotFound, "Not Found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok", "message": "Welcome to the Mock Server!"}`))
		logRequest(r, http.StatusOK, "Success")
	})

	// 2. ログインエンドポイント (POST /login)
	// クライアントを認証し、Cookieをセットしてセッションを開始します。
	http.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			logRequest(r, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}

		var payload struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		err := json.NewDecoder(r.Body).Decode(&payload)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error": "invalid json"}`))
			logRequest(r, http.StatusBadRequest, "Invalid JSON")
			return
		}

		if payload.Username == "" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error": "username required"}`))
			logRequest(r, http.StatusBadRequest, "Missing Username")
			return
		}

		// 簡単なセッション管理のためのCookie（session_id）を発行
		http.SetCookie(w, &http.Cookie{
			Name:    "session_id",
			Value:   "sess_" + payload.Username,
			Path:    "/",
			Expires: time.Now().Add(24 * time.Hour),
		})

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fmt.Sprintf(`{"status": "success", "user": "%s"}`, payload.Username)))
		logRequest(r, http.StatusOK, fmt.Sprintf("Login Success (user: %s)", payload.Username))
	})

	// 3. プロフィール閲覧エンドポイント (GET /profile)
	// Cookieを検証し、ログイン中のユーザー情報を返します。未認証時は 401 Unauthorized を返します。
	http.HandleFunc("/profile", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			logRequest(r, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}

		cookie, err := r.Cookie("session_id")
		if err != nil || cookie.Value == "" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "unauthorized"}`))
			logRequest(r, http.StatusUnauthorized, "Missing or Invalid Session Cookie")
			return
		}

		username := strings.TrimPrefix(cookie.Value, "sess_")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fmt.Sprintf(`{"username": "%s", "email": "%s@example.com", "role": "user"}`, username, username)))
		logRequest(r, http.StatusOK, fmt.Sprintf("Profile Access Success (user: %s)", username))
	})

	// サーバーをポート 8080 で起動
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
