CLIENT_COUNT ?= 3

.PHONY: build run-test report clean help

help:
	@echo "仮想ネットワーク複数クライアント再現ツール コマンド一覧:"
	@echo "  make build      - 各種コンテナイメージのビルド"
	@echo "  make run-test   - 検証サーバーの起動と複数クライアントのテスト実行"
	@echo "                    (例: make run-test CLIENT_COUNT=5)"
	@echo "  make report     - 実行結果の集計レポートを表示"
	@echo "  make clean      - テスト結果の削除とコンテナの停止"

build:
	docker compose build

run-test:
	@echo "古いテスト結果をクリアしています..."
	rm -rf results
	mkdir -p results
	@echo "検証サーバーを起動中..."
	docker compose up -d server
	@echo "サーバーの起動を待機しています (2s)..."
	sleep 2
	@echo "$(CLIENT_COUNT)台のクライアントコンテナから負荷テストを実行します..."
	docker compose up --scale client=$(CLIENT_COUNT) client
	@echo "テスト完了。検証サーバーを停止しています..."
	docker compose stop server

report:
	@echo "テスト結果を集計中..."
	docker compose run --rm manager

clean:
	docker compose down -v
	rm -rf results
