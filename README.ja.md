# LINE CLI：個人のLINEアカウント向けコマンドラインクライアント

[English](README.md) | [繁體中文（台灣）](README.zh-TW.md) | 日本語 | [ภาษาไทย](README.th.md)

**LINE CLI** は、Goで書かれた非公式のオープンソースLINEコマンドラインクライアントです。
ターミナルからLINEメッセージを送信し、個人やグループのトークを閲覧したり、
ファイルを共有したり、添付ファイルをダウンロードしたり、リアルタイムイベントを
受信したりできます。日常の操作には対話式の入力を、シェルスクリプトや
AIエージェントのワークフローにはJSON出力を利用できます。

**macOS、Linux、Windows**に対応し、トークが対応している場合はLetter Sealingで
エンドツーエンド暗号化を行います。個人のLINEアカウントでログインでき、
ボットアカウントの作成やLINE Messaging APIの設定は不要です。

[![CI](https://github.com/kongesque/line-cli/actions/workflows/cli.yml/badge.svg)](https://github.com/kongesque/line-cli/actions/workflows/cli.yml)
[![Release](https://img.shields.io/github/v/release/kongesque/line-cli?filter=v*&label=release)](https://github.com/kongesque/line-cli/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/kongesque/line-cli)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[インストール](#install-line-cli) · [クイックスタート](#quick-start-send-your-first-line-message) · [コマンド](#line-messaging-commands) · [自動化](#automate-line-with-json-and-shell-scripts) · [詳しいガイド](docs/CLI.ja.md)

![LINE CLI：ターミナルから個人のLINEでメッセージ送信、ファイル共有、自動化](banner.png)

<a id="install-line-cli"></a>

## LINE CLIのインストール

### macOS：Homebrew

```sh
brew install kongesque/tap/line-cli
```

### macOS／Linux：単体バイナリのインストーラー

```sh
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.sh | sh
```

インストーラーがOSとCPUアーキテクチャを判別し、リリースのチェックサムを検証して、
`line` を `~/.local/bin` にインストールします。`PATH` 設定を反映するための案内が
表示された場合は、ターミナルを開き直してください。

**Linuxデスクトップ：** ログイン前に、`line` と同じD-Busセッション内で
Secret Serviceキーリングが起動し、ロックが解除されていることを確認してください。
Debian／Ubuntuでは次のツールをインストールします。

```sh
sudo apt install libsecret-tools gnome-keyring
```

**Linuxサーバー／SSH：** パッケージのインストールだけでは不十分です。対応するホストでは、
`line login --headless` でロック解除済みキーリングを必要としないホスト鍵ストレージを
利用できます。初期設定は対話式で、このストレージはディスク全体のコピーに対する保護や
TPM保護を提供しません。[ヘッドレス環境の設定、移行、サービス運用](docs/CLI.ja.md#linux-servers-and-headless-storage)を参照してください。

### Windows：PowerShell

```powershell
irm https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.ps1 | iex
```

インストーラーがチェックサムを検証し、`line.exe` をユーザーの `PATH` に追加します。
必要に応じてPowerShellを開き直してください。Windowsでは追加のキーリング用パッケージは不要です。

[リリースをダウンロード](https://github.com/kongesque/line-cli/releases/latest)するか、
[ソースからビルド](#build-and-contribute)することもできます。リリース版バイナリは
署名・公証されていません。

インストールを確認します。

```sh
line version
line help
```

<a id="quick-start-send-your-first-line-message"></a>

## クイックスタート：最初のLINEメッセージを送信

> [!IMPORTANT]
> ログインすると、既存のLINE Chrome拡張機能や、Chrome形式を使う別のクライアントの
> セッションが置き換えられる場合があります。保存できるアカウントはOSユーザーごとに1つです。

### 1. スマートフォンのLINEでログイン

```sh
line login
```

スマートフォンのLINEのスキャナーでターミナルのQRコードを読み取り、ログインを承認します。
PINの入力を求められた場合は、表示された番号を入力してください。
**Session saved securely** と表示されるまで待ってください。QRログインは実験的な機能で、
Letter Sealingが有効になっている必要があります。

アカウントにメールアドレスとパスワードを設定済みの場合は、次の方法も使えます。

```sh
line login --email you@example.com
```

パスワードは画面に表示せずに入力でき、保存されません。ログイン時にはストレージを検査し、
利用可能な保存済みセッションを置き換える前に確認します。
[ログインのオプションとQRコードのトラブルシューティング](docs/CLI.ja.md#login-options-and-qr-help)も参照してください。

### 2. トークを閲覧してメッセージを送信

```sh
line whoami     # ログイン中のアカウントを確認
line chats      # トーク一覧を表示
line messages   # トークを選んで最近のメッセージを閲覧
line send       # 送信先を選んでメッセージを入力
```

対話式ターミナルでは、必要な情報が不足しているとCLIが入力を促します。
Ctrl-Cでキャンセルできます。次の例のようにトーク名を直接指定することもできます。

<a id="line-messaging-commands"></a>

## LINEメッセージの操作コマンド

| 操作 | コマンド |
| --- | --- |
| 友だちを検索 | `line contacts --search "Alice"` |
| ユーザーIDで人物を検索 | `line contacts --mid USER_MID` |
| トークやグループを検索 | `line chats --search "Family"` |
| 最近のメッセージを閲覧 | `line messages "Alice" --limit 20` |
| テキストメッセージを送信 | `line send "Alice" --text "Hello!"` |
| ファイルを送信 | `line send "Alice" --file ./report.pdf` |
| 写真、動画、音声、ファイルをダウンロード | `line download` |
| リアクションを追加・削除 | `line react` |
| 自分のメッセージを送信取消 | `line unsend` |
| リアルタイムイベントを受信 | `line watch` |
| ローカルのセッションストレージを検査 | `line auth status --check` |
| 保存済みのローカルセッションを削除 | `line logout` |

例の名前は実際の名前に置き換えてください。名前は重複のない完全一致で指定します
（大文字・小文字は区別しません）。空白を含む名前は引用符で囲んでください。
名前が重複する場合は、`line chats --search "Alice" --show-ids` で調べて、完全なトークIDを指定します。

オプションは `line COMMAND --help` で確認できます。ダウンロード、リアクション、
送信取消も対話式の選択に対応しています。[トークの選択ガイド](docs/CLI.ja.md#find-chats-and-people)も参照してください。

### LINEメッセージへの返信と添付ファイルのダウンロード

`--show-ids` でIDを確認し、例の `MESSAGE_ID` を置き換えます。

```sh
line messages "Alice" --show-ids
line send "Alice" --text "Sounds good" --reply-to MESSAGE_ID
line react "Alice" --message MESSAGE_ID --reaction love
line download "Alice" --message MESSAGE_ID --output ./received.pdf
```

ダウンロードには添付ファイルのメッセージIDを指定し、適切なファイル名を選んでください。
既存のファイルは上書きされません。
[リアクション、送信取消、ダウンロードの詳細](docs/CLI.ja.md#download-or-change-a-message)を参照してください。

<a id="automate-line-with-json-and-shell-scripts"></a>

## JSONとシェルスクリプトでLINEを自動化

`--json` を付けると、構造化データをstdoutに、診断メッセージをstderrに出力します。
`--json` と `--stdin` は対話式の入力を無効にするため、必要な引数をすべて指定してください。
名前は変更されることがあるので、スクリプトでは完全なトークIDを使います。

```sh
line chats --search "Family" --json
line messages CHAT_ID --limit 20 --json
line send CHAT_ID --stdin --json < message.txt
line watch --json > events.ndjson
```

`CHAT_ID` は `line chats --json` で取得します。`--stdin` はファイルやパイプから
複数行のテキストを読み取れます。`watch` は1行につき1件のJSON（NDJSON）を出力し、
保存済みチェックポイントから再開します。受信側ではrevisionで重複を除去してください。
出力したメッセージやイベントは私的なデータとして扱ってください。

リモート側の状態を変更する操作は1回だけ試行され、自動では再試行されません。
送信、アップロード、リアクション、送信取消の応答を受け取れなかった場合は、
操作の重複を避けるため、再実行する前にLINE側の状態を確認してください。

連携の詳細は[JSONフィールドと終了コード](docs/CLI.ja.md#json-output-and-exit-codes)と
[リアルタイムイベントの受信](docs/CLI.ja.md#watch-new-events)を参照してください。

## Letter Sealingによる暗号化とセッションのセキュリティ

- **暗号化：** 対応する場合はLetter Sealingを使用します。鍵の不足、形式の不正、
  通信障害によって、暗号化した送信が通知なく平文に切り替わることはありません。
  JSONの送信結果で暗号化の状態を確認できます。
- **ストレージ：** macOSはキーチェーン、Linuxは鍵をSecret Serviceに保存する
  AES-GCMセッションファイル、Windowsは現在のユーザー用DPAPIを使用します。
  ヘッドレスLinuxはホスト鍵ストレージを使用します。
- **セッションの復旧：** 保存済みの更新用認証情報が有効なら、再起動後もアクセストークンが
  自動更新されます。更新でアクセスを復旧できない場合や、LINEがセッションを無効化した場合は、
  再度ログインしてください。

[セッションの保存先](docs/CLI.ja.md#where-your-session-is-stored) ·
[トークンの更新と復旧](docs/CLI.ja.md#token-refresh)

## 制限事項

- 閲覧できるのは最近の履歴のみで、1回につき最大100件です。閲覧しても既読にはなりません。
  古い暗号化メッセージの一部は閲覧できない場合があります。
- ファイル送信とメディアのダウンロードは最大20 MiBです。`--file` で送信した画像・動画・音声は
  通常のファイルとして表示されます。スタンプやメディア専用メッセージの送信、
  外部メディアURLからのダウンロードには対応していません。
- LINE CLIはLINEのChrome形式のプロトコルを使用します。サーバー側の変更によって
  互換性に影響が出る場合があり、一部の処理にはより幅広い実環境での検証が必要です。

## LINE CLIの更新

アップグレード前に実行中のコマンドとイベント監視プロセスを停止してください。
新旧バージョンの同時実行には対応していません。

```sh
line update --check   # インストールせずに最新版を確認
line update           # 対応する場合は更新し、それ以外は手順を表示
```

macOSとLinuxで公式インストーラーから導入した単体バイナリは、その場で更新できます。
Homebrewの場合は `brew upgrade line-cli` を使用します。その他のインストール方法では
更新手順が表示されます。確認にLINEへのログインは不要で、コマンドを実行したときだけ
確認します。[更新の詳細](docs/CLI.ja.md#updating-line-cli)を参照してください。

## ドキュメントとサポート

- [CLIガイド](docs/CLI.ja.md)：全コマンドの使い方、トラブルシューティング、サーバー設定。
- [トークンとセッションの監査](TOKEN_SESSION.md)：更新と復旧の実装詳細。
- [GitHub Issues](https://github.com/kongesque/line-cli/issues)：不具合報告と機能リクエスト。

不具合の報告には `line version`、OS、実行したコマンド、機密情報を除いたエラーを添えてください。
パスワード、トークン、QRログインの値、私的なメッセージは含めないでください。

<a id="build-and-contribute"></a>

## ビルドと開発への参加

ソースからのビルドにはGitと **Go 1.26+** が必要です。macOSではキーチェーンを
利用するためにXcode Command Line ToolsとCGOも必要です。macOS／Linuxでは次を実行します。

```sh
git clone https://github.com/kongesque/line-cli.git
cd line-cli
./install.sh
```

インストーラーが表示するPATHの案内に従ってください。Windows PowerShellの手順は
[ソースビルドガイド](docs/CLI.ja.md#build-from-source)を参照してください。
ソースからビルドする場合も、Linuxのストレージ要件が適用されます。

ローカルで開発する場合：

```sh
./build.sh
./bin/line help
go test -race ./internal/... ./cmd/line ./pkg/line/... ./pkg/e2ee
go vet ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
```

全チェック項目とプロトコルの要件は[コントリビューター向けガイド](AGENTS.md)を参照してください。
CIは3つのプラットフォームとOS標準の認証情報ストレージを検査します。
テストには合成データと偽のAPIを使い、実際のLINEを使うテストには明示的な許可が必要です。

## ライセンスと由来

LINE CLIはLINEと提携しておらず、LINEの承認を受けたものでもありません。
[beeper/line](https://github.com/beeper/line)をもとに開発されており、共有するプロトコルと
暗号化コードには、アップストリームの著作権表示と由来を保持しています。
MatrixコネクターとBeeperのデプロイ環境は、このプロジェクトには含まれません。

[MIT License](LICENSE)のもとで配布されています。
