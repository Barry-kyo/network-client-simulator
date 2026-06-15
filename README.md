# 仮想ネットワーク複数クライアント再現ツール (Virtual Network Load Simulator)

本ツールは、仮想ネットワーク（Dockerブリッジネットワーク）上で、それぞれ異なるIPアドレスとデバイス特性（User-Agent等）を持った複数のクライアントからのHTTP/HTTPSアクセスをシミュレートし、対象サーバーの挙動や負荷を検証するための負荷テストツールです。

---

## 主な特徴

- **IPアドレスの分散シミュレート**: Dockerのブリッジネットワークを利用し、各クライアントコンテナに個別のIPアドレスを割り当てることで、サーバー側からはそれぞれ異なる端末・ロケーションからのアクセスに見えるように再現します。
- **デバイスプロファイルのランダム割当**: `iPhone/Safari`、`Android/Chrome`、`Windows/Chrome`、`Mac/Firefox` などの代表的なデバイスの User-Agent とヘッダーの組み合わせをクライアントのセッションごとにランダムに適用します。
- **シナリオベースの並行ユーザー**: `config.yaml` で定義したリクエストシーケンス（ホーム画面遷移 ➔ ログイン ➔ プロフィール取得など）を、指定した同時接続数（VU）でループ実行します。
- **状態（セッション）の維持**: CookieJar を内蔵しているため、ログインステップで返されたセッションCookieなどを後続のリクエストステップへ自動的に引き継ぎます。
- **統合レポート生成**: 各クライアントコンテナから書き出された実行結果ファイルをマネージャーツールが瞬時に集約し、平均レイテンシや 90%/95%/99% パーセンタイル、HTTPステータス分布、ステップ別成功率を色付きのコンソールレポートで表示します。
- **ポータビリティ**: すべて Docker / Docker Compose 内でビルド・実行されるため、ホストマシンに Go 言語のコンパイル環境がインストールされていなくても動作します。

---

## ディレクトリ構造

```text
.
├── config.yaml          # テストシナリオやターゲットの設定
├── docker-compose.yml   # クライアントコンテナおよび検証サーバーの定義
├── Makefile             # ビルド・実行・集計の統合コマンド
├── Dockerfile.client    # クライアントシミュレータのビルド定義
├── Dockerfile.server    # 検証用モックサーバーのビルド定義
├── Dockerfile.manager   # 結果集計マネージャーツールのビルド定義
├── docs/
│   └── spec.md          # 詳細仕様書
├── client/              # クライアントシミュレータ (Go)
│   ├── main.go          # メイン処理 (Goroutine制御、HTTPクライアント実行)
│   └── device.go        # デバイスプロファイルの定義
├── server/              # 検証用モックサーバー (Go)
│   └── main.go          # リクエストIPとUAをログ出力するWebサーバー
└── manager/             # 結果集計マネージャーツール (Go)
    └── main.go          # ログ集約とコンソール統計レポート出力
```

---

## クイックスタート

### 前提条件
- Docker および Docker Compose V2 がインストールされていること

### 1. 各イメージのビルド
```bash
make build
```

### 2. ロードテストの実行
自動的に検証用モックサーバーが起動し、3台のクライアントコンテナからテストを実行します（設定ファイル内の `duration_seconds` に基づき実行後、自動的に終了します）。
```bash
make run-test
```

### 3. 集計レポートの表示
実行完了後、結果ログをマージしたサマリーレポートを画面に表示します。
```bash
make report
```

### 4. クリーンアップ
一時結果ファイルや実行コンテナ、ボリュームを完全に消去します。
```bash
make clean
```

---

## 高度な使い方

### クライアント（コンテナ）数を変更する
実行時の引数として `CLIENT_COUNT` を渡すことで、クライアント数を動的に変更できます（デフォルトは `3` です）。
```bash
# クライアントコンテナを 5 台にスケールアウトして実行
make run-test CLIENT_COUNT=5
```

### 外部サーバーへの負荷テストを実施する
環境変数 `TARGET_URL` を指定することで、コンテナに環境変数が転送され、デフォルトのモックサーバー以外の任意の宛先に対してテストを実行できます。
```bash
# 外部の検証対象サーバーにアクセスし、クライアントコンテナを10台で実行する例
TARGET_URL=http://your-target-server:8000 make run-test CLIENT_COUNT=10
```

---

## テストシナリオのカスタマイズ

[config.yaml](file:///home/ogawadaiki/workspace/network-client/config.yaml) を編集することで、リクエスト順序やパラメータを定義できます。

```yaml
# 対象サーバーのベースURL (検証サーバーを使用する場合は 'http://server:8080')
target_url: "http://server:8080"

# テストパラメータ
duration_seconds: 15
vus_per_client: 3
think_time_ms: 1000

# 実行シナリオ (各VUが上から順にステップを実行します)
scenario:
  - name: "Access Home Page"
    method: "GET"
    path: "/"
    expect_status: 200

  - name: "Submit Login"
    method: "POST"
    path: "/login"
    # {{VU_ID}} プレースホルダーは仮想ユーザーの個別識別名に自動置換されます
    body: '{"username": "user_{{VU_ID}}", "password": "password123"}'
    headers:
      Content-Type: "application/json"
    expect_status: 200
    think_time_ms: 500

  - name: "Get Profile Data"
    method: "GET"
    path: "/profile"
    expect_status: 200
    think_time_ms: 800
```
