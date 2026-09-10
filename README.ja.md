# LINE CLI：ターミナルから個人のLINEを操作

[繁體中文（台灣）](README.zh-TW.md) | [ภาษาไทย](README.th.md) | 日本語 | [English](README.md)

**LINE CLI** は、個人のLINEアカウントをターミナルから操作するための
非公式コマンドラインクライアントです。メッセージの閲覧・送信をはじめ、
ファイルの共有、返信、リアクション、送信取消、リアルタイムイベントの監視に
対応しています。JSONを利用すれば、シェルスクリプトやAIエージェントの
ワークフローに組み込むこともできます。

LINE Messaging APIを使う一般的なツールとは異なり、ボットアカウントではなく、
普段お使いの個人アカウントで動作します。

![個人のLINEをターミナルから操作できるLINE CLI](banner.png)

[![CI](https://github.com/kongesque/line-cli/actions/workflows/cli.yml/badge.svg)](https://github.com/kongesque/line-cli/actions/workflows/cli.yml)
[![Release](https://img.shields.io/github/v/release/kongesque/line-cli?filter=cli-v*&label=release)](https://github.com/kongesque/line-cli/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/kongesque/line-cli)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

macOS、Linux、Windowsに対応し、Letter Sealingによるエンドツーエンド暗号化と、
OS標準の認証情報ストレージを利用できます。

> [!IMPORTANT]
> LINEで同時に利用できるChrome形式のセッションは1つだけです。`line` で
> ログインすると、既存のLINE Chrome拡張機能や、同じ形式を使う別のクライアントの
> セッションが置き換えられる場合があります。また、保存できるアカウントは
> OSユーザーごとに1つです。

## 主な機能

- 個人のLINEアカウントをターミナルから直接操作
- 友だち、グループ、トークを検索
- 最近のメッセージ履歴を表示
- 1行または複数行のテキストメッセージを送信
- 最大20 MiBのファイルを送信
- 既存のメッセージに返信
- 標準リアクションを追加・削除
- 自分が送ったメッセージを送信取消
- LINEのリアルタイムイベントをNDJSON形式でストリーミング
- シェルスクリプトや自動化処理向けにJSONを出力
- OS標準の認証情報ストレージで保存済みセッションを保護
- トークが対応している場合はLetter Sealingで暗号化

## LINE CLIのインストール

### macOS／Linux

次のコマンドで最新版をインストールできます。

```sh
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.sh | sh
```

インストーラーがOSとCPUアーキテクチャを自動判別し、リリースのチェックサムを
検証したうえで、`line` を `~/.local/bin` にインストールします。一般的なシェルの
`PATH` 設定も自動で行います。案内が表示された場合は、ターミナルを開き直してください。

Linuxでは、`line login` を実行する前に認証情報ストレージをインストールしてください。
Debian／Ubuntuの場合は次のコマンドを実行します。

```sh
sudo apt install libsecret-tools gnome-keyring
```

同じD-Busセッション内でSecret Serviceキーリングが起動し、ロックが解除されている
必要があります。

> [!IMPORTANT]
> SSH接続やヘッドレス環境のLinuxでは、パッケージをインストールするだけでは
> 不十分です。CLIが使用するD-Busセッション内にロック解除済みのSecret Serviceが
> ない場合、スマートフォンでの本人確認後に `could not save Secret Service` と
> 表示され、ログインに失敗します。

macOSではHomebrewも利用できます。

```sh
brew install kongesque/tap/line-cli
```

Homebrewはタグ付きリリースをソースからビルドし、`line` コマンドをインストールします。
この方法なら、署名されていないバイナリをブラウザからダウンロードした際に表示される
Gatekeeperの警告を回避できます。

後からアップグレードする場合は、次のコマンドを実行します。

```sh
brew upgrade line-cli
```

### Windows（PowerShell）

PowerShellで次のコマンドを実行します。

```powershell
irm https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.ps1 | iex
```

インストーラーがCPUアーキテクチャを自動判別し、リリースのチェックサムを検証してから、
`line.exe` をユーザープロファイル内にインストールし、ユーザーの `PATH` に追加します。
Windowsでは標準搭載のDPAPIを使用するため、キーリング用パッケージは不要です。
現時点ではリリース版バイナリに署名はありません。インストール直後に `line` を実行できない
場合は、PowerShellを開き直してください。

### ソースからビルド

ビルドにはGitとGo 1.26以降が必要です。macOS／Linuxでは次のコマンドを実行します。

```sh
git clone https://github.com/kongesque/line-cli.git
cd line-cli
./install.sh
line help
```

Windowsでは、[PowerShell用のビルド手順](CLI.md#build-from-source)を参照してください。
`install.sh` の実行にはPOSIXシェルが必要なため、PowerShell上では直接動作しません。

ソースからビルドした場合も、Linuxでは前述のキーリング環境が必要です。
Windows PowerShellでの手順やコントリビューター向けのチェック項目については、
[ソースビルドガイド](CLI.md#build-from-source)を参照してください。

## ターミナルからLINEを使う

LINEアカウントにメールアドレスとパスワードが設定されている必要があります。
ログインは対話形式で行い、スマートフォン側での承認も必要です。

```sh
line login
line whoami
line chats
line messages "Family group" --limit 10
line send "Alice" --text "Hello!"
```

名前を引数で指定する場合は、重複のない完全一致でなければなりません。
対象を指定せずにコマンドを実行すると、トークを対話形式で選択できます。

```sh
line messages
line send
line react
line unsend
```

そのほか、よく使う操作は次のとおりです。

```sh
line send "Alice" --file ./report.pdf
line send "Alice" --text "Sounds good" --reply-to MESSAGE_ID
line react "Alice" --message MESSAGE_ID --reaction love
line download "Alice" --message MESSAGE_ID --output ./received.pdf
```

オプションや使用例を確認するには、`line COMMAND --help` を実行してください。

## JSONと自動化

```sh
line chats --search "Family" --json
line messages "Alice" --limit 20 --json
line send "Alice" --stdin < message.txt
line watch --json > events.ndjson
```

JSONデータは標準出力、診断メッセージは標準エラー出力に書き出されます。
エクスポートしたメッセージやイベントは、私的な会話データとして取り扱ってください。

リモート側の状態を変更する操作は1回だけ実行され、自動で再試行されることはありません。
送信やアップロード後に応答を受け取れなかった場合は、重複を防ぐため、再実行する前に
LINE側の状態を確認してください。

## セキュリティとプライバシー

アカウントとトークが対応している場合、LINE CLIはLetter Sealingを使用します。
鍵が見つからない場合やネットワーク障害が発生した場合でも、暗号化した送信が
通知なく平文へ切り替わることはありません。送信結果には暗号化の有無が表示されます。

パスワードが保存されることはありません。セッションはOSの機能で保護されます。

| プラットフォーム | 認証情報の保存先 |
| --- | --- |
| macOS | キーチェーン |
| Linux | 暗号化用の鍵をSecret Serviceで保護するAES-GCMセッションファイル |
| Windows | 現在のユーザー用DPAPI |

LINE CLIは、LINEのChrome形式のプロトコルをもとに実装されています。
サーバー側の変更によって互換性に影響が出る場合があります。

## 現在の制限事項

- 保存できるアカウントはOSユーザーごとに1つ
- 閲覧できるのは最近の履歴のみで、1回につき最大100件
- ファイル送信のみ（スタンプやメディア専用メッセージには未対応）
- メッセージを表示しても既読にはならない
- より幅広い実環境での検証が必要なプロトコル処理が一部残っている

## ドキュメント

- [CLIの全コマンドリファレンスとトラブルシューティング](CLI.md)
- [Letter Sealingの実装に関する補足](readme/LETTER_SEALING.md)
- [コントリビューター向けガイドとパッケージ構成](AGENTS.md)

## 開発

```sh
./build.sh
go test -race ./internal/... ./cmd/line ./pkg/line/... ./pkg/e2ee
go vet ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
```

CIはLinux、macOS、Windowsで実行され、OS標準の認証情報ストレージに関する
チェックも含まれます。

## ライセンスと由来

LINE CLIはLINEの公式製品ではなく、LINEとの提携や承認を受けたものでもありません。
[beeper/line](https://github.com/beeper/line)をもとに開発されており、共有している
プロトコルおよび暗号化コードには、アップストリームの著作権表示と由来を保持しています。
MatrixコネクターとBeeperのデプロイ環境は、このプロジェクトには含まれません。

[MIT License](LICENSE)のもとで配布されています。
