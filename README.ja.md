# LINE CLI：個人のLINEアカウント向けコマンドラインクライアント

[English](README.md) | [繁體中文（台灣）](README.zh-TW.md) | 日本語 | [ภาษาไทย](README.th.md)

**LINE CLI** は、個人のLINEアカウントをターミナルから使うためのGo製の非公式CLIです。
トークの閲覧、メッセージやファイルの送信、添付ファイルのダウンロード、イベントの受信ができます。
**macOS、Linux、Windows**に対応し、対話形式の操作とJSON出力を利用できます。
ボットアカウントやLINE Messaging APIの設定は不要です。

[![CI](https://github.com/kongesque/line-cli/actions/workflows/cli.yml/badge.svg)](https://github.com/kongesque/line-cli/actions/workflows/cli.yml)
[![Release](https://img.shields.io/github/v/release/kongesque/line-cli?filter=v*&label=release)](https://github.com/kongesque/line-cli/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/kongesque/line-cli)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[インストール](#install-line-cli) · [クイックスタート](#quick-start-send-your-first-line-message) · [コマンド](#line-messaging-commands) · [自動化](#automate-line-with-json-and-shell-scripts) · [詳しいガイド](docs/CLI.ja.md)

![LINE CLI：ターミナルから個人のLINEでメッセージ送信、ファイル共有、自動化](banner.png)

このREADMEは開発ブランチの説明です。リリース済みバージョンについては該当タグのREADMEと
`line COMMAND --help` を確認してください。[変更履歴](CHANGELOG.md)も参照してください。

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
現在のシェルですぐに使うには `export PATH="$HOME/.local/bin:$PATH"` を実行します。

**Linuxデスクトップ：** DebianやUbuntuでは、ログイン前に認証情報の保存用パッケージを導入します。

```sh
sudo apt install libsecret-tools gnome-keyring
```

`line` はデスクトップセッションから実行し、同じD-BusセッションでSecret Serviceキーリングが
動作し、ロックが解除されていることを確認してください。デスクトップのキーリングがない
サーバーやSSHでは、下記のヘッドレス環境でのログインを使ってください。

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
**Session saved securely** と表示されるまで待ってください。
QRログインを利用するには、Letter Sealingが有効になっている必要があります。

LINEアカウントにメールアドレスとパスワードを設定済みなら、次の方法も使えます。QRログインに失敗した場合も利用できます。

```sh
line login --email you@example.com
```

パスワードは入力中も画面に表示されません。求められた場合はスマートフォンで認証を完了してください。
パスワードは保存されません。

<details>
<summary>LinuxサーバーやSSH：デスクトップのキーリングなしでログイン</summary>

通常のUnixユーザーと対話式ターミナルを使ってください。リモート接続では `ssh -t user@host` を使えます。
ヘッドレスストレージには、systemd 256–259の `/usr/bin/systemd-creds` と、
動作するユーザー認証情報ブローカーが必要です。

```sh
line login --headless
```

メールアドレスとパスワードを使う場合は `line login --headless --email you@example.com` を実行します。
確認時に **Host key; no TPM** に同意してください。この保存方式はディスク全体のコピーに対する保護や
TPM保護を提供しません。初回の設定にはターミナルでの対話操作とスマートフォンでの承認が必要です。

**Session saved securely** と表示されたら `line auth status --check` を実行します。
以降は同じUnixユーザーで下記のコマンドを実行し、`--headless` は付けません。

</details>

### 2. トークを閲覧してメッセージを送信

```sh
line whoami     # ログイン中のアカウントを確認
line chats      # トーク一覧を表示
line messages   # トークを選んで最近のメッセージを閲覧
line send       # 送信先を選んでメッセージを入力
```

対話式ターミナルでは、必要な情報が不足しているとCLIが入力を促します。
Ctrl-Cでキャンセルできます。次の例のようにトーク名を直接指定することもできます。

送信に成功すると `Sent to … · end-to-end encrypted` と表示されます。
非E2EE送信の場合は `Sent to … · transport encrypted (no E2EE)` と表示されます。

<a id="line-messaging-commands"></a>

## LINEメッセージの操作コマンド

| 操作 | コマンド |
| --- | --- |
| 友だちを検索 | `line contacts --search "Alice"` |
| 最近のメッセージを閲覧 | `line messages "Alice" --limit 20` |
| テキストメッセージを送信 | `line send "Alice" --text "Hello!"` |
| ファイルを送信 | `line send "Alice" --file ./report.pdf` |
| 写真、動画、音声、ファイルをダウンロード | `line download` |
| リアルタイムイベントを受信 | `line watch` |

`Alice` は完全なトークID、または他と重複しないトーク名に置き換えてください。名前は全体を指定し、大文字と小文字は区別しません。
同じ名前が複数ある場合は `line chats --search "Alice" --show-ids` を実行し、表示された完全なIDを使います。
オプションは `line COMMAND --help`、返信やリアクション、ページ分割は
[CLIガイド](docs/CLI.ja.md#daily-commands)で確認できます。

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

`CHAT_ID` は `line chats --json` で取得します。イベント出力はNDJSONで、受信側はrevisionで重複を除いてください。
フィールドとエラーの形式は[JSONリファレンス](docs/JSON.md)を参照してください。

サーバーの状態を変更する操作は1回だけ試行します。送信などの応答を受け取れなかった場合は、
繰り返す前にLINEで結果を確認してください。

## Letter Sealingによる暗号化とセッションのセキュリティ

トークとメッセージの種類が対応していれば、LINE CLIはLINEのエンドツーエンド暗号化「Letter Sealing」を使います。
LINEがLetter Sealingに非対応と明示した場合も、HTTPS/TLSで転送中のメッセージを保護します。
Letter Sealingを利用すると、LINEのサーバーもメッセージの内容を読み取れません。
すべての送信でエンドツーエンド暗号化が必要な場合は、`--require-e2ee` を指定してください。

[送信と暗号化](docs/CLI.ja.md#send-messages-and-files)・
[セッションの保存](docs/CLI.ja.md#where-your-session-is-stored)を参照してください。

## 制限事項

- Letter Sealingの対応範囲は、トークとメッセージの種類によって異なります。
  LINEが非対応と明示した場合は、HTTPS/TLSで通信を暗号化して送信します。
  詳しくは[LINE暗号化レポート2025（英語）](https://www.lycorp.co.jp/en/privacy-security/security/transparency/encryption-report/2025/)を参照してください。
- 履歴は1ページ最大100件です。`--page --json` とカーソルで取得可能な古い履歴へ進めますが、
  全履歴は保証しません。閲覧しても既読にはならず、古い暗号化メッセージの一部は読めない場合があります。
- ファイル送信とメディアのダウンロードは最大20 MiBです。`--file` は元のバイト列を保持し、通常のファイルとして表示されます。
  暗号化した画像メッセージの送信は無効です。非E2EE画像は、LINEが対応を明示した場合に限り送信できます。
  動画・音声の専用送信、スタンプ送信、外部メディアURLのダウンロードは非対応です。
- LINE CLIはLINEのChrome形式のプロトコルを使用します。サーバー側の変更によって
  互換性に影響が出る場合があり、一部の処理にはより幅広い実環境での検証が必要です。

## LINE CLIの更新

アップグレード前に実行中のコマンドとイベント監視プロセスを停止してください。
新旧バージョンの同時実行には対応していません。

```sh
line update --check   # インストールせずに最新版を確認
line update           # 対応する場合は更新し、それ以外は手順を表示
```

Homebrewでは `brew upgrade line-cli` を実行します。その他のインストール方法は
[更新ガイド](docs/CLI.ja.md#updating-line-cli)を参照してください。

## ドキュメントとサポート

[CLIガイド](docs/CLI.ja.md)または[ドキュメント一覧](docs/README.md)を参照してください（リファレンスは英語です）。
不具合や機能要望は [GitHub Issues](https://github.com/kongesque/line-cli/issues) へ。
不具合の報告には `line version`、OS、コマンド、機密情報を除いたエラーを添えてください。
パスワード、トークン、QRログインの値、私的なメッセージは含めないでください。

<a id="build-and-contribute"></a>

## ビルドと開発への参加

ソースからのビルドには **Go 1.27.2** が必要です。macOSではXcode Command Line ToolsとCGOも必要です。
設定と開発時の検証は[ソースからのビルド](docs/CLI.ja.md#build-from-source)と
[開発への参加](CONTRIBUTING.md)（英語）を参照してください。

## ライセンスと由来

LINE CLIはLINEと提携しておらず、LINEの承認を受けたものでもありません。
[beeper/line](https://github.com/beeper/line)をもとに開発されており、共有するプロトコルと
暗号化コードには、アップストリームの著作権表示と由来を保持しています。
MatrixコネクターとBeeperのデプロイ環境は、このプロジェクトには含まれません。

[MIT License](LICENSE)のもとで配布されています。
