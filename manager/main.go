package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ANSIエスケープシーケンス：ターミナル出力のテキスト装飾用カラーコード。
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorCyan   = "\033[36m"
	ColorBold   = "\033[1m"
)

// RequestLog はクライアントから出力される個々のリクエストログと同一の構造です。
type RequestLog struct {
	Timestamp  time.Time `json:"timestamp"`
	VU         string    `json:"vu"`
	StepName   string    `json:"step_name"`
	Method     string    `json:"method"`
	URL        string    `json:"url"`
	StatusCode int       `json:"status_code"`
	LatencyMs  int64     `json:"latency_ms"`
	Success    bool      `json:"success"`
	Error      string    `json:"error,omitempty"`
}

// ClientResults はクライアントファイル構造体にマッピングされます。
type ClientResults struct {
	Hostname string       `json:"hostname"`
	Logs     []RequestLog `json:"logs"`
}

// StepStats は各シナリオステップごとの詳細メトリクスを集計する構造体です。
type StepStats struct {
	Name        string          // ステップ名
	Requests    int             // 総リクエスト数
	Successes   int             // 成功リクエスト数
	Latencies   []int64         // レイテンシのリスト（パーセンタイル計算用）
	StatusCodes map[int]int     // ステータスコードごとの出現回数
	Errors      map[string]int  // エラー内容ごとの出現回数
}

func main() {
	// 結果ファイルのディレクトリを指定（引数がない場合は /results）
	resultsDir := "/results"
	if len(os.Args) > 1 {
		resultsDir = os.Args[1]
	}

	fmt.Printf("\n%s%s=== Load Test Aggregation Manager ===%s\n", ColorBold, ColorCyan, ColorReset)
	fmt.Printf("Reading results from: %s\n\n", resultsDir)

	// 'client-*.json' というパターンにマッチする全ログファイルを検索
	files, err := filepath.Glob(filepath.Join(resultsDir, "client-*.json"))
	if err != nil || len(files) == 0 {
		log.Fatalf("No client results found in %s (error: %v)", resultsDir, err)
	}

	var allLogs []RequestLog
	var clients []string

	// 各ファイルを読み込んでログデータをマージ
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			log.Printf("Failed to read file %s: %v", file, err)
			continue
		}

		var res ClientResults
		if err := json.Unmarshal(data, &res); err != nil {
			log.Printf("Failed to parse JSON from %s: %v", file, err)
			continue
		}

		clients = append(clients, res.Hostname)
		allLogs = append(allLogs, res.Logs...)
	}

	if len(allLogs) == 0 {
		log.Fatalf("No request logs collected from client files.")
	}

	// 統計集計用の変数
	totalRequests := len(allLogs)
	successes := 0
	var overallLatencies []int64
	statusCodes := make(map[int]int)
	errorsMap := make(map[string]int)

	stepStatsMap := make(map[string]*StepStats)

	minTime := allLogs[0].Timestamp
	maxTime := allLogs[0].Timestamp

	// 全ログを走査してメトリクスを集計
	for _, req := range allLogs {
		// テスト全体の開始時間・終了時間を割り出し（テスト期間の算出用）
		if req.Timestamp.Before(minTime) {
			minTime = req.Timestamp
		}
		if req.Timestamp.After(maxTime) {
			maxTime = req.Timestamp
		}

		if req.Success {
			successes++
		}
		overallLatencies = append(overallLatencies, req.LatencyMs)
		statusCodes[req.StatusCode]++
		if req.Error != "" {
			// 長すぎるエラー文字列は表示を丸める
			errKey := req.Error
			if len(errKey) > 120 {
				errKey = errKey[:117] + "..."
			}
			errorsMap[errKey]++
		}

		// ステップ別の集計
		stats, ok := stepStatsMap[req.StepName]
		if !ok {
			stats = &StepStats{
				Name:        req.StepName,
				StatusCodes: make(map[int]int),
				Errors:      make(map[string]int),
			}
			stepStatsMap[req.StepName] = stats
		}
		stats.Requests++
		if req.Success {
			stats.Successes++
		}
		stats.Latencies = append(stats.Latencies, req.LatencyMs)
		stats.StatusCodes[req.StatusCode]++
		if req.Error != "" {
			errKey := req.Error
			if len(errKey) > 120 {
				errKey = errKey[:117] + "..."
			}
			stats.Errors[errKey]++
		}
	}

	// テスト時間とRPS（秒間リクエスト数）の計算
	testDuration := maxTime.Sub(minTime)
	if testDuration.Seconds() < 1 {
		testDuration = time.Second // 1秒未満のゼロ除算防止
	}

	overallRPS := float64(totalRequests) / testDuration.Seconds()
	successRate := (float64(successes) / float64(totalRequests)) * 100

	// パーセンタイル計算のために全体レイテンシを昇順ソート
	sort.Slice(overallLatencies, func(i, j int) bool { return overallLatencies[i] < overallLatencies[j] })

	// --- 全体情報出力 ---
	fmt.Printf("%s--- General Information ---%s\n", ColorBold, ColorReset)
	fmt.Printf("Active Client Containers: %s%d%s (%v)\n", ColorBlue, len(clients), ColorReset, clients)
	fmt.Printf("Test Duration:            %s%.2fs%s\n", ColorBlue, testDuration.Seconds(), ColorReset)
	fmt.Printf("Total Requests:           %s%d%s\n", ColorBlue, totalRequests, ColorReset)
	fmt.Printf("Requests/sec (RPS):       %s%.2f%s\n", ColorBlue, overallRPS, ColorReset)
	fmt.Printf("Success Rate:             ")
	if successRate == 100.0 {
		fmt.Printf("%s100.00%%%s\n", ColorGreen, ColorReset)
	} else if successRate > 95.0 {
		fmt.Printf("%s%.2f%%%s\n", ColorYellow, successRate, ColorReset)
	} else {
		fmt.Printf("%s%.2f%%%s\n", ColorRed, successRate, ColorReset)
	}
	fmt.Println()

	// --- レイテンシ統計出力 ---
	fmt.Printf("%s--- Latency Statistics ---%s\n", ColorBold, ColorReset)
	if len(overallLatencies) > 0 {
		avg := calcAverage(overallLatencies)
		minL := overallLatencies[0]
		maxL := overallLatencies[len(overallLatencies)-1]
		p50 := getPercentile(overallLatencies, 0.50)
		p90 := getPercentile(overallLatencies, 0.90)
		p95 := getPercentile(overallLatencies, 0.95)
		p99 := getPercentile(overallLatencies, 0.99)

		fmt.Printf("  Min:   %s%4d ms%s\n", ColorGreen, minL, ColorReset)
		fmt.Printf("  Mean:  %s%4.1f ms%s\n", ColorCyan, avg, ColorReset)
		fmt.Printf("  50th:  %s%4d ms%s (median)\n", ColorCyan, p50, ColorReset)
		fmt.Printf("  90th:  %s%4d ms%s\n", ColorYellow, p90, ColorReset)
		fmt.Printf("  95th:  %s%4d ms%s\n", ColorYellow, p95, ColorReset)
		fmt.Printf("  99th:  %s%4d ms%s\n", ColorRed, p99, ColorReset)
		fmt.Printf("  Max:   %s%4d ms%s\n", ColorRed, maxL, ColorReset)
	} else {
		fmt.Println("  No latencies recorded.")
	}
	fmt.Println()

	// --- ステータスコード分布出力 ---
	fmt.Printf("%s--- HTTP Status Codes ---%s\n", ColorBold, ColorReset)
	for code, count := range statusCodes {
		pct := (float64(count) / float64(totalRequests)) * 100
		color := ColorGreen
		if code >= 400 {
			color = ColorRed
		} else if code >= 300 {
			color = ColorYellow
		}
		fmt.Printf("  [%d] %s%d%s requests (%.1f%%)\n", code, color, count, ColorReset, pct)
	}
	fmt.Println()

	// --- シナリオステップごとの内訳出力 ---
	fmt.Printf("%s--- Scenario Step Breakdown ---%s\n", ColorBold, ColorReset)
	// 表示順を固定するため、キー（ステップ名）をソート
	var stepNames []string
	for name := range stepStatsMap {
		stepNames = append(stepNames, name)
	}
	sort.Strings(stepNames)

	for _, name := range stepNames {
		s := stepStatsMap[name]
		sort.Slice(s.Latencies, func(i, j int) bool { return s.Latencies[i] < s.Latencies[j] })
		sPct := (float64(s.Successes) / float64(s.Requests)) * 100
		avg := calcAverage(s.Latencies)
		p95 := getPercentile(s.Latencies, 0.95)

		sColor := ColorGreen
		if sPct < 100 {
			sColor = ColorYellow
		}
		if sPct < 90 {
			sColor = ColorRed
		}

		fmt.Printf("  %s%s%s:\n", ColorCyan, s.Name, ColorReset)
		fmt.Printf("    Requests:     %d\n", s.Requests)
		fmt.Printf("    Success Rate: %s%.2f%%%s\n", sColor, sPct, ColorReset)
		fmt.Printf("    Avg Latency:  %.1f ms\n", avg)
		fmt.Printf("    95th Latency: %d ms\n", p95)
		fmt.Println()
	}

	// --- 発生したエラーサマリー出力 ---
	if len(errorsMap) > 0 {
		fmt.Printf("%s--- Error Summary ---%s\n", ColorBold, ColorRed)
		for errMsg, count := range errorsMap {
			fmt.Printf("  - %s%s%s (occurred %d times)\n", ColorYellow, errMsg, ColorReset, count)
		}
		fmt.Println()
	}
}

// calcAverage は int64 スライスの平均値を計算して float64 で返します。
func calcAverage(nums []int64) float64 {
	if len(nums) == 0 {
		return 0
	}
	var sum int64
	for _, n := range nums {
		sum += n
	}
	return float64(sum) / float64(len(nums))
}

// getPercentile は昇順ソート済みのスライスから指定したパーセンタイル値を取得します。
func getPercentile(sortedNums []int64, pct float64) int64 {
	if len(sortedNums) == 0 {
		return 0
	}
	idx := int(math.Ceil(pct * float64(len(sortedNums))))
	if idx == 0 {
		return sortedNums[0]
	}
	if idx > len(sortedNums) {
		return sortedNums[len(sortedNums)-1]
	}
	return sortedNums[idx-1]
}
