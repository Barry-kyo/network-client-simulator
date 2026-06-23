package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Config は config.yaml の設定値をマッピングする構造体です。
type Config struct {
	TargetURL       string         `yaml:"target_url"`       // 対象サーバーのベースURL
	DNSServer       string         `yaml:"dns_server"`       // 実験用DNSサーバー (IP:Port)
	TargetDomain    string         `yaml:"target_domain"`    // ターゲットドメイン名
	TargetPort      int            `yaml:"target_port"`      // ターゲットポート
	DeviceProfile   string         `yaml:"device_profile"`   // 固定するデバイスプロファイル名
	DurationSeconds int            `yaml:"duration_seconds"` // テスト全体の継続時間（秒）
	VUsPerClient    int            `yaml:"vus_per_client"`    // コンテナごとの同時起動仮想ユーザー（VU）数
	ThinkTimeMs     int            `yaml:"think_time_ms"`     // デフォルトのステップ間の待機時間（ミリ秒）
	Scenario        []ScenarioStep `yaml:"scenario"`         // 実行する一連のHTTPリクエスト定義
}

// ScenarioStep はシナリオ内の1つのHTTPリクエストステップを表します。
type ScenarioStep struct {
	Name         string            `yaml:"name"`           // ステップ識別名（例: Access Home Page）
	Method       string            `yaml:"method"`         // HTTPメソッド（GET, POST 等）
	Path         string            `yaml:"path"`           // エンドポイントパス
	Body         string            `yaml:"body"`           // 送信するリクエストボディ（プレースホルダー対応）
	Headers      map[string]string `yaml:"headers"`        // カスタムHTTPヘッダー
	ExpectStatus int               `yaml:"expect_status"`  // 期待するHTTPステータスコード
	ThinkTimeMs  *int              `yaml:"think_time_ms"`  // 本ステップ完了後の個別待機時間（省略時は全体デフォルトを適用）
}

// RequestLog は個別のHTTPリクエスト結果のロギング用構造体です。
type RequestLog struct {
	Timestamp  time.Time `json:"timestamp"`        // リクエスト開始時刻
	VU         string    `json:"vu"`               // 仮想ユーザー識別名
	StepName   string    `json:"step_name"`        // 実行ステップ名
	Method     string    `json:"method"`           // HTTPメソッド
	URL        string    `json:"url"`              // リクエストURL
	UserAgent  string    `json:"user_agent"`       // 使用したUser-Agent (追加)
	StatusCode int       `json:"status_code"`      // レスポンスのHTTPステータスコード
	LatencyMs  int64     `json:"latency_ms"`       // レイテンシ（ミリ秒）
	Success    bool      `json:"success"`          // リクエストの成否（ステータスコードおよびエラー有無で判定）
	Error      string    `json:"error,omitempty"`  // 失敗時のエラー内容
}

// ClientResults は、1つのクライアントコンテナの全結果をまとめる構造体です。
type ClientResults struct {
	Hostname      string        `json:"hostname"`       // クライアントのホスト名
	DeviceProfile DeviceProfile `json:"device_profile"` // プロセス全体で使用したデバイスプロファイル (追加)
	Logs          []RequestLog  `json:"logs"`           // 全実行ログの配列
}

func main() {
	// ホスト名を取得（結果ログのファイル名やVU識別子に使用）
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown-client"
	}
	log.Printf("[%s] Starting client simulator...", hostname)

	// config.yaml 設定ファイルの読み込み
	configData, err := os.ReadFile("config.yaml")
	if err != nil {
		log.Fatalf("Failed to read config.yaml: %v", err)
	}

	var config Config
	if err := yaml.Unmarshal(configData, &config); err != nil {
		log.Fatalf("Failed to parse config.yaml: %v", err)
	}

	// 環境変数 TARGET_URL がセットされている場合、設定ファイルの値を上書きします
	if envTarget := os.Getenv("TARGET_URL"); envTarget != "" {
		config.TargetURL = envTarget
	}

	log.Printf("[%s] Target URL: %s, VUs: %d, Duration: %ds",
		hostname, config.TargetURL, config.VUsPerClient, config.DurationSeconds)

	// すべてのVUの実行ログを安全に集約するためのチャネル
	resultsChan := make(chan RequestLog, 10000)
	var logs []RequestLog

	// すべてのVU（Goroutine）の終了を待機するためのWaitGroup
	var wg sync.WaitGroup

	// 指定された実行時間でテストを打ち切るための Context
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.DurationSeconds)*time.Second)
	defer cancel()

	// プロセス全体で一貫したデバイスプロファイルを選択
	var processProfile DeviceProfile
	if config.DeviceProfile != "" {
		found := false
		for _, p := range DeviceProfiles {
			if p.Name == config.DeviceProfile {
				processProfile = p
				found = true
				break
			}
		}
		if !found {
			log.Printf("[%s] Device profile '%s' not found, selecting randomly", hostname, config.DeviceProfile)
			processProfile = GetRandomDeviceProfile()
		}
	} else {
		processProfile = GetRandomDeviceProfile()
	}
	log.Printf("[%s] Selected device profile for this process: %s (UA: %s)", hostname, processProfile.Name, processProfile.UserAgent)

	// VUsPerClient で指定された数の Goroutine (仮想ユーザー) を起動
	for i := 1; i <= config.VUsPerClient; i++ {
		vuID := fmt.Sprintf("%s-vu-%d", hostname, i)
		wg.Add(1)
		go runVirtualUser(ctx, vuID, config, processProfile, resultsChan, &wg)
	}

	// ログ収集用 Goroutine
	collectorDone := make(chan struct{})
	go func() {
		for logEntry := range resultsChan {
			logs = append(logs, logEntry)
		}
		close(collectorDone)
	}()

	// 全VUの終了を待ち、チャネルを閉じる
	wg.Wait()
	close(resultsChan)
	<-collectorDone

	// 収集したログデータをシリアライズして共有ディレクトリに保存
	results := ClientResults{
		Hostname:      hostname,
		DeviceProfile: processProfile,
		Logs:          logs,
	}

	err = os.MkdirAll("/results", 0755)
	if err != nil {
		log.Fatalf("Failed to create /results directory: %v", err)
	}

	outputPath := fmt.Sprintf("/results/client-%s.json", hostname)
	outputFile, err := os.Create(outputPath)
	if err != nil {
		log.Fatalf("Failed to create output file: %v", err)
	}
	defer outputFile.Close()

	encoder := json.NewEncoder(outputFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(results); err != nil {
		log.Fatalf("Failed to write results JSON: %v", err)
	}

	log.Printf("[%s] Simulation complete. Wrote %d request logs to %s", hostname, len(logs), outputPath)
}

// runVirtualUser は1人の仮想ユーザーの振る舞いをシミュレートする Goroutine です。
func runVirtualUser(ctx context.Context, vuID string, config Config, profile DeviceProfile, resultsChan chan<- RequestLog, wg *sync.WaitGroup) {
	defer wg.Done()

	log.Printf("[%s] Running as %s (UA: %s)", vuID, profile.Name, profile.UserAgent)

	// クッキー維持（ログイン後のセッション維持）用の CookieJar を備えた HTTP クライアントを作成
	jar, _ := cookiejar.New(nil)
	var httpClient *http.Client

	if config.DNSServer != "" && config.TargetDomain != "" {
		// 実験用の明示的DNS解決クライアントを作成
		dnsPort := config.DNSServer
		if strings.Count(dnsPort, ":") > 1 && !strings.HasPrefix(dnsPort, "[") {
			// IPv6アドレスで角括弧がない場合 (例: "2400:4109:100:500::53") -> "[2400:4109:100:500::53]:53"
			dnsPort = "[" + dnsPort + "]:53"
		} else if strings.HasPrefix(dnsPort, "[") && strings.HasSuffix(dnsPort, "]") {
			// 角括弧はあるがポートがない場合 (例: "[2400:4109:100:500::53]") -> "[2400:4109:100:500::53]:53"
			dnsPort = dnsPort + ":53"
		} else if !strings.Contains(dnsPort, ":") {
			// IPv4アドレスでポートがない場合 (例: "192.168.10.53") -> "192.168.10.53:53"
			dnsPort = dnsPort + ":53"
		}

		resolver := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{
					Timeout: 2 * time.Second,
				}
				netType := network
				if strings.Contains(dnsPort, "[") && !strings.HasSuffix(netType, "6") {
					netType = netType + "6"
				}
				return d.DialContext(ctx, netType, dnsPort)
			},
		}

		dialer := &net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 0,
		}

		transport := &http.Transport{
			DisableKeepAlives: true, // リクエストごとの新規DNS解決を強制
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					host = addr
					port = fmt.Sprintf("%d", config.TargetPort)
				}

				// DNS AAAA クエリを実行
				ips, err := resolver.LookupIP(ctx, "ip6", host)
				if err != nil || len(ips) == 0 {
					log.Printf("[%s] AAAA lookup failed for %s, trying IPv4: %v", vuID, host, err)
					ips, err = resolver.LookupIP(ctx, "ip4", host)
					if err != nil || len(ips) == 0 {
						return nil, fmt.Errorf("DNS lookup failed for %s: %v", host, err)
					}
				}

				destIP := ips[0].String()
				destAddr := net.JoinHostPort(destIP, port)

				netType := "tcp6"
				if ips[0].To4() != nil {
					netType = "tcp"
				}

				return dialer.DialContext(ctx, netType, destAddr)
			},
		}

		httpClient = &http.Client{
			Jar:       jar,
			Timeout:   10 * time.Second,
			Transport: transport,
		}

		// 動的DNS解決のため、TargetURLを書き換えてドメイン宛てにアクセスする
		scheme := "http"
		if config.TargetPort == 443 {
			scheme = "https"
		}
		config.TargetURL = fmt.Sprintf("%s://%s", scheme, config.TargetDomain)
	} else {
		// 従来のフォールバッククライアント
		httpClient = &http.Client{
			Jar:     jar,
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		}
	}

	// タイムアウト時間までシナリオをループ実行
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// シナリオのステップを順に実行
			for _, step := range config.Scenario {
				select {
				case <-ctx.Done():
					return
				default:
					executeStep(ctx, vuID, httpClient, profile, config.TargetURL, step, config.ThinkTimeMs, resultsChan)
				}
			}
		}
	}
}

// executeStep は個別のリクエストステップをビルド・実行し、結果をチャネルに送信します。
func executeStep(ctx context.Context, vuID string, client *http.Client, profile DeviceProfile, targetURL string, step ScenarioStep, defaultThinkTimeMs int, resultsChan chan<- RequestLog) {
	url := targetURL + step.Path

	// リクエストボディのプレースホルダー置換 ({{VU_ID}} -> 固有のVU名)
	bodyStr := step.Body
	bodyStr = strings.ReplaceAll(bodyStr, "{{VU_ID}}", vuID)

	var reqBody io.Reader
	if bodyStr != "" {
		reqBody = bytes.NewBufferString(bodyStr)
	}

	// リクエストオブジェクトの生成
	req, err := http.NewRequestWithContext(ctx, step.Method, url, reqBody)
	if err != nil {
		resultsChan <- RequestLog{
			Timestamp: time.Now(),
			VU:        vuID,
			StepName:  step.Name,
			Method:    step.Method,
			URL:       url,
			UserAgent: profile.UserAgent,
			Success:   false,
			Error:     fmt.Sprintf("failed to create request: %v", err),
		}
		return
	}

	// 割り当てられたデバイスのデフォルトヘッダーを設定
	req.Header.Set("User-Agent", profile.UserAgent)
	for k, v := range profile.Headers {
		req.Header.Set(k, v)
	}

	// ステップ固有のヘッダー（例: Content-Type）を上書き設定
	for k, v := range step.Headers {
		req.Header.Set(k, v)
	}

	// リクエスト送信と時間計測
	startTime := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(startTime).Milliseconds()

	logEntry := RequestLog{
		Timestamp: startTime,
		VU:        vuID,
		StepName:  step.Name,
		Method:    step.Method,
		URL:       url,
		UserAgent: profile.UserAgent,
		LatencyMs: latency,
	}

	if err != nil {
		logEntry.Success = false
		logEntry.Error = err.Error()
		resultsChan <- logEntry
		return
	}
	defer resp.Body.Close()

	logEntry.StatusCode = resp.StatusCode
	expectedStatus := step.ExpectStatus
	if expectedStatus == 0 {
		expectedStatus = 200
	}

	// 結果の成功・失敗判定
	if resp.StatusCode == expectedStatus {
		logEntry.Success = true
	} else {
		logEntry.Success = false
		// エラーハンドリング用にレスポンスボディの先頭100文字を抽出してエラーログに付与
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 100))
		logEntry.Error = fmt.Sprintf("Expected status %d, got %d. Body: %s", expectedStatus, resp.StatusCode, string(bodyBytes))
	}

	resultsChan <- logEntry

	// Think Time（思考時間・リクエスト間待機）
	thinkTime := defaultThinkTimeMs
	if step.ThinkTimeMs != nil {
		thinkTime = *step.ThinkTimeMs
	}

	if thinkTime > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(time.Duration(thinkTime) * time.Millisecond):
		}
	}
}
