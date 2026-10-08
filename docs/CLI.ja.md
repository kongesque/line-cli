# LINE CLIガイド

[English](CLI.md) | [繁體中文（台灣）](CLI.zh-TW.md) | 日本語 | [ภาษาไทย](CLI.th.md)

ターミナルから個人のLINEアカウントを使えます。トークの閲覧、メッセージやファイルの送信、
添付ファイルのダウンロード、新しいイベントの受信に対応しています。
ターミナルでは対話式の案内に従って操作でき、スクリプトではJSON出力を利用できます。

> [!IMPORTANT]
> LINEで同時に使えるChrome形式のセッションは1つです。このCLIでログインすると、
> 既存のLINE Chrome拡張機能や、同じ形式を使う別のクライアントのセッションが置き換えられる場合があります。

<a id="get-started"></a>

## はじめに

<a id="install"></a>

### インストール

macOSまたはLinux：

```sh
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.sh | sh
```

インストーラーが適切なリリースを選び、チェックサムを検証して、`line` を `~/.local/bin` に
インストールします。一般的なシェルの `PATH` も設定します。案内が表示された場合は、
ターミナルを開き直してください。

macOSではHomebrewも利用できます。

```sh
brew install kongesque/tap/line-cli
```

DebianまたはUbuntuでは、ログイン前に認証情報の保存に必要なツールをインストールしてください。

```sh
sudo apt install libsecret-tools gnome-keyring
```

Linuxのキーリングは、`line` と同じD-Busセッション内で起動し、ロックが解除されている必要があります。
SSH経由で使う場合や、ロック解除済みキーリングのないサーバーでは、
[Linuxサーバーとヘッドレスストレージ](#linux-servers-and-headless-storage)を参照してください。

WindowsではPowerShellで実行します。

```powershell
irm https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.ps1 | iex
```

インストーラーが `line.exe` を `%LOCALAPPDATA%\line-cli\bin` に配置し、
そのディレクトリをユーザーの `PATH` に追加します。必要に応じてPowerShellを開き直してください。

リリース版バイナリは現在、署名されていません。
[GitHub Releases](https://github.com/kongesque/line-cli/releases/latest)で確認・ダウンロードできます。

インストールを確認します。

```sh
line version
line help
```

<a id="sign-in-and-send-your-first-message"></a>

### ログインして最初のメッセージを送信

```sh
line login
line whoami
line chats
line messages "Family group"
line send "Alice" --text "Hello!"
```

`line login` でQRコードが表示されます。スマートフォンのLINEで読み取り、ログインを承認して、
求められた場合は表示されたPINを入力してください。
**Session saved securely** と表示されるまで、ほかのコマンドは実行しないでください。

QRログインは実験的な機能です。LINEアカウントにメールアドレスとパスワードを設定している場合は、
次の方法も使えます。

```sh
line login --email you@example.com
```

パスワードの入力内容は表示されず、保存もされません。パスワード用のオプションや環境変数はありません。

<a id="login-options-and-qr-help"></a>

### ログインのオプションとQRコードのトラブルシューティング

| コマンド・オプション | 用途 |
| --- | --- |
| `line login` または `line login --qr` | ターミナルのQRコードでログインします。 |
| `line login --email ADDRESS` | メールアドレス・パスワードとスマートフォンの認証でログインします。 |
| `line login --qr-url` | 信頼できるローカルのQRツールに、一度限りのQRデータを渡します。 |
| `line login --force` | 保存済みセッションを置き換えるかどうかの確認を省略します。 |
| `line login --headless` | ロック解除済みキーリングのない、対応Linuxホストで初期登録します。 |

`--email` と `--qr`／`--qr-url` は併用できません。どちらのQR方式も対話入力できる
ターミナルが必要ですが、stdoutのリダイレクトは可能です。進行状況、QRコード、PIN、警告は
stderrに出力し、最後の成功メッセージはstdoutに出力します。

QRコードはLINEのスキャナーで読み取ってください。PINは先頭のゼロも含め、表示どおりに入力します。
スマートフォンで承認しただけでは、CLIのプロフィール確認、鍵のエクスポート、セッション保存は完了していません。

ターミナルの幅が足りない場合は、必要な幅と `--qr-url` の案内が表示されます。
一部が欠けたQRコードは表示しません。`NO_COLOR` を設定すると、明示的な色指定を無効にできます。
`--qr-url` の値は秘密情報として扱い、信頼できるローカルツールだけで使用してください。
共有したり、オンラインのQR生成サービスに送ったり、保存したりしないでください。
値はターミナルのスクロール履歴には残ります。QR画像は保存されません。
通常のQRログイン前に `QRCODE_DEBUG` を解除してください。QRエンコーダーのデバッグモードでは
データが書き出されることがあります。

表示される残り時間は目安です。LINEが有効期限切れを確認すると、新しいQRコードを表示できます。
1回のログインで最大3個までです。ネットワークエラーやPINのタイムアウトでは再試行しません。
簡易ターミナルやリダイレクトしたstderrでは、カウントダウンの代わりに状態の変化を表示します。

QRログインは現在、Letter Sealingを無効にしたアカウントには対応していません。
保存済みQR証明書が拒否されてもPIN認証には切り替わらず、未知の証明書エラーでもログインを停止します。
証明書の検証エラーが出た場合は、メールアドレスでのログインを試してください。
`Diagnostic: verifyCertificate` という補足がある場合、含まれるのは数値コードだけです。
QRデータ、PIN、証明書、トークン、サーバーの応答本文は含みません。

<a id="saved-sessions-and-cancellation"></a>

### 保存済みセッションとキャンセル

ログインはLINEに接続する前にローカルストレージを確認し、利用可能な保存済みセッションを
置き換える前に確認します。**no** と答えると保持できます。`--force` が省略するのはこの確認だけです。
ストレージの確認、ヘッドレスストレージへの同意、セッションの同時利用確認、プロトコルエラーは省略しません。

Ctrl-Cで入力やQRのポーリングをキャンセルできます。SIGINTの終了コードは130、SIGTERMは143です。
終了前にパスワード入力のエコー設定を元に戻します。従来のメール認証リクエストには、
既存のHTTPタイムアウトが引き続き適用されます。

新しいセッションを保存する前にキャンセルすると、古いローカルセッションは残ります。
ただし、最後のログインリクエストがLINEに届いていれば、古いChrome形式のセッションは
すでに置き換えられている可能性があります。このリクエストは自動で再試行しません。
保存結果が不確かな場合は `line auth status --check` で確認し、古いコピーの復元や、
確認せずにログインを繰り返すことは避けてください。保存成功後にシグナルを受けた場合は、成功を報告します。

SSHでは対話式ターミナルを使ってください。

```sh
ssh -t user@host
line login
# メールアドレスでログインする場合：
line login --email you@example.com
```

SSHでも認証情報の保存先は必要です。対応Linuxホストにロック解除済みキーリングがない場合は、
[ヘッドレスストレージ](#linux-servers-and-headless-storage)を利用できます。

<a id="daily-commands"></a>

## 日常のコマンド

<a id="let-the-cli-guide-you"></a>

### 案内に従って操作する

対話式ターミナルでは、一部の引数を省略できます。

```sh
line messages
line send
line download
line react
line unsend
```

番号でトークやメッセージを選ぶか、名前を検索します。`n` と `p` でページを移動し、
`q` またはCtrl-Cでキャンセルできます。`--json` と `--stdin` では入力の案内が無効になるため、
スクリプトでは必要な引数をすべて指定してください。

<a id="find-chats-and-people"></a>

### トークやユーザーを探す

```sh
line chats
line chats --search "Family"
line chats --all --limit 50
line chats --show-ids
line contacts
line contacts --search "Alice"
```

`chats` はトークを、`contacts` は友だちを表示します。通常の表示では、どちらも初期値は20件です。
JSONでは `--limit` を指定しない限り、すべての一致結果を表示します。
通常の表示で全件を出すには `--limit 0` を使います。`chats --all` は非アクティブなトークも含みます。

`CHAT` を取るコマンドには、連絡先・ルーム・グループの完全なID、または一意に完全一致する名前を指定できます。
名前の大文字・小文字は区別しません。スペースのある名前は引用符で囲んでください。
名前が見つからない場合や同名のトークが複数ある場合は、`line chats --search NAME --show-ids` で
完全なIDを確認します。名前は変わることがあるため、スクリプトではIDを使うほうが確実です。

友だち一覧にないユーザーを調べるには、そのユーザーの完全なMID（LINEのユーザー識別子）を使います。
例えば、`line messages CHAT_ID --json` のグループメッセージにある `from` は、送信者のMIDです。

```sh
line contacts --mid USER_MID
line contacts --mid FIRST_USER_MID --mid SECOND_USER_MID --json
```

ユーザーごとに `--mid` を指定します。指定されたIDだけを調べ、入力順を保ち、完全に同じIDの重複を除き、
要求したすべてのIDを表示します。友だちには追加しません。グループ／ルームID、表示名、公開のLINE IDではなく、
完全なユーザーMIDを使ってください。従来の `u` ＋小文字の16進数32文字と、
現在の `u`／`U` ＋base64urlの43文字に対応しています。

通常の表示では、各MIDと名前を並べます。自分で設定した連絡先名がプロフィール名より優先されます。
`(unavailable)` はLINEがプロフィールを返さなかった場合、`(name unavailable)` は
プロフィールは返したものの、有効な名前がない場合です。どちらもアカウント削除の判断には使えません。
`--mid` と `--search`／`--limit` は併用できません。スクリプトでの扱いは
[JSONのユーザー検索結果](#json-lookup-results)を参照してください。

<a id="read-messages"></a>

### メッセージを読む

```sh
line messages "Alice"
line messages "Alice" --limit 50
line messages "Alice" --show-ids
line messages CHAT_ID --json
```

`messages` は直近の1〜100件を取得し、古い順に表示します。
`--show-ids` で、返信、リアクション、ダウンロード、送信取消に必要なIDを表示できます。

履歴を読んでも既読にはならず、本文をローカルに保存することもありません。
元の端末の鍵をLINEから取得できなくなった場合、古い暗号化メッセージの一部は読めないことがあります。

<a id="send-messages-and-files"></a>

### メッセージやファイルを送信する

```sh
line send "Alice" --text "Hello!"
line send "Alice" --stdin < message.txt
line send "Alice" --text "Sounds good" --reply-to MESSAGE_ID
line send "Family group" --file ./report.pdf
line send "Family group" --file ./report.pdf --reply-to MESSAGE_ID
```

`--text`、`--stdin`、`--file` のいずれか1つを指定します。テキストは空にできず、
上限はUTF-16で10,000コード単位です。一般ファイルは20 MiBまで送信できます。
`--file` で送る画像・動画・音声は、通常のファイルとして表示されます。

対応するトークではLetter Sealingを使います。鍵の不足、グループメンバー情報の不足、通信の失敗があれば、
暗号化送信を停止し、勝手に平文へ切り替えることはありません。
新しいグループ鍵を登録できるのは、明示的なグループ送信で、現在の全メンバーを把握できている場合だけです。

`--json` を付けると、メッセージID、暗号化の有無、グループ鍵登録の有無、リクエストの連番を確認できます。

> [!CAUTION]
> 送信やその他のリモート変更は、それぞれ1回だけ試行します。応答が届かなかった場合は、
> 再試行する前にLINEで確認してください。操作がすでに成功している可能性があります。

<a id="download-or-change-a-message"></a>

### 添付ファイルのダウンロードとメッセージ操作

`line messages CHAT --show-ids` または `--json` でメッセージIDを確認します。

```sh
line download "Alice" --message MESSAGE_ID --output ./received.pdf
line download "Alice" --message IMAGE_MESSAGE_ID --output ./photo.jpg
line react "Alice" --message MESSAGE_ID --reaction love
line react "Alice" --message MESSAGE_ID --remove
line unsend "Alice" --message MY_MESSAGE_ID
```

リアクションは `like`、`love`、`laugh`、`surprise`、`sad`、`angry` です。
送信取消は自分のメッセージに限られ、LINEサーバーのルールが適用されます。

これらのコマンドは、選んだトークの直近100件から対象を探します。
ダウンロードで既存ファイルを上書きすることはありません。暗号化データは保存前に検証し、
リモートのメタデータが保存先のパスを決めることもありません。

`download` は画像・動画・音声・一般ファイルのメッセージに対応し、上限は20 MiB、
転送タイムアウトは2分です。メディアの全バイトをそのまま保存するため、拡張子を変えても形式は変換されません。
外部の `DOWNLOAD_URL` を持つメッセージは非対応です。閲覧やダウンロードでグループ鍵は登録しません。

添付ファイルのデータを別のコマンドに渡す場合は、トークとメッセージのIDを明示します。

```sh
# Bashなど、pipefailに対応するシェルで実行：
set -o pipefail
line download CHAT_ID --message MESSAGE_ID --output - | consumer
```

`consumer` を使用するプログラムに置き換えてください。このモードではstdoutに添付データだけを出し、
診断情報はstderrに出します。対話入力と `--json` は利用できません。
バイナリをターミナルに直接出力することも拒否します。`-` という名前のファイルを作りたい場合は
`--output ./-` を指定してください。

暗号化メディアは、最初の1バイトを出す前に認証・検証します。ダウンロードや認証に失敗した場合、
添付データは出力しません。ただし、出力開始後のパイプ切断やキャンセルでは途中までのデータが残ることがあるため、
終了コードを確認し、`pipefail` を使ってください。ファイル保存には `--output PATH` を推奨します。
シェルの `>` では、CLIが動く前に既存ファイルが切り詰められることがあります。

<a id="watch-new-events"></a>

### 新しいイベントを受信する

```sh
line watch
line watch --timeout 30s
line watch --limit 10
line watch --from-now
```

`watch` はstdoutに1行1件のJSONイベントを、stderrに状態メッセージを出力します。
初回はLINEの現在のrevisionから始まり、次回以降は保存済みチェックポイントから再開します。
`--from-now` を指定するとチェックポイントを破棄し、新しいイベントから始めます。

| イベント | 意味 |
| --- | --- |
| `message` | メッセージの送信または受信。 |
| `operation` | その他のLINE通知。 |
| `resync_required` | LINEがイベントの欠落を通知。トークとメッセージを再取得してください。 |

受信側では `revision` を使って重複を除いてください。イベント出力後、チェックポイントの保存前に
プロセスが止まると、最後のイベントが再び出る可能性があります。同時に動かせるwatcherは1つです。

<a id="updating-line-cli"></a>

## LINE CLIの更新

```sh
line update --check
line update
line update --check --json
```

`update` はインストール済みバージョン、GitHubの最新安定版、インストール方法、
検出した実行ファイルを表示します。LINEへのログインは不要です。
`--check` は確認と次の手順の表示だけを行い、インストールやセッションロックの取得はしません。
メッセージ操作のコマンドでは更新を確認しません。

| インストール方法 | `line update` の動作 |
| --- | --- |
| macOS／Linuxの公式単体インストーラー | 新しい安定版があればダウンロード・検証し、実行ファイルを置き換えます。 |
| Homebrew | `brew upgrade line-cli` を表示します。実行ファイルはHomebrewが管理します。 |
| Windowsの公式単体インストーラー | このコマンドの終了後に実行するPowerShellのインストールコマンドを表示します。 |
| ソースからのビルド | リリースタグとソースビルドの手順を案内します。 |
| 不明なインストール方法 | リリースページを表示し、元の方法で更新するよう案内します。 |

自動更新には公式リリースビルドと、公式インストーラーが実行ファイルの隣に作成する
`.line-cli-install` というインストール記録の両方が必要です。手動でコピーしたバイナリ、
記録のない古いインストール、ソースビルドは自動で置き換えません。元の方法で更新してください。
公式インストーラーを再実行すると記録も作成されます。単体版はカスタムのインストール先にも対応します。
Windowsの更新コマンドは、検出したディレクトリを `LINE_CLI_INSTALL_DIR` に設定し、保存先を維持します。

更新前に、すべてのコマンドとwatcherを停止してください。watcherを再起動するサービスも対象です。
自動置き換えは認証情報を読まずにwatcherとセッションのロックを取得し、
どちらかを取得できなければ続行しません。外部のインストーラーを同時に実行しないでください。

更新処理は確認済みリリースタグからファイルを取得し、そのリリースのSHA-256チェックサムで
アーカイブを検証します。同じディレクトリに実行ファイルを準備して、アトミックな名前変更で置き換えます。
ダウンロード、チェックサム検証、展開に失敗しても既存の実行ファイルは保持します。
ダウンロードと展開にはサイズ制限があります。sudo、インストールの再試行、新しい版からのダウングレード、
開発版・プレリリース版の順序の推測は行いません。

`--json` はstdoutに結果を1件出力し、診断情報をstderrに出します。インストールの動作は通常の表示と同じです。
確認だけを行うには、**`--check` と `--json` を両方指定**してください。
フィールドは `current_version`、`latest_version`、`status`、`installation`、`executable`、
`can_self_update`、`release_url`、および任意の `instructions`、`upgrade_command` です。

確認時の状態は `update_available`、`up_to_date`、`ahead`、`unknown_version` です。
インストール成功は `updated`、失敗は `failed` です。`updated_unconfirmed` は置き換えが済んだものの、
ディレクトリの同期ができなかったことを示します。再試行前に `line version` で確認してください。
`current_version` は常にコマンド起動時の版を記録し、`updated` 後の `latest_version` は
インストールされた版です。`can_self_update` は自動更新の対応可否を示し、新版の有無は示しません。

確認成功と手動更新の案内は、新版がある場合も終了コード0です。確認やインストールの失敗は非ゼロです。
最初の確認に失敗した場合はJSONを出しません。インストール失敗時は結果を出してからエラー終了します。
キャンセル時は通常のシグナル終了コードを使います。

<a id="use-from-scripts"></a>

## スクリプトから使う

<a id="json-output-and-exit-codes"></a>

### JSON出力と終了コード

対応するコマンドに `--json` を付けます。

```sh
line whoami --json
line contacts --json
line contacts --mid USER_MID --json
line chats --search "Family" --json
line messages CHAT_ID --json
line send CHAT_ID --stdin --json < message.txt
```

JSONはstdout、診断情報はstderrに出力します。成功とヘルプは終了コード0、
通常のCLIエラーとネットワークエラーは1です。ストレージエラーには
[専用の終了コード](#storage-exit-codes)があります。SIGINTは130、SIGTERMは143です。
空の結果一覧は `[]` です。

| コマンド | 主なJSON情報 |
| --- | --- |
| `whoami` | プロフィールとアカウントID。 |
| `contacts` | 友だちの名前とID。 |
| `contacts --mid MID` | 重複を除いた要求MIDごとに1件。検索状態を含みます。 |
| `chats` | ID、種類、未読数、任意の活動時刻。 |
| `messages` | ID、送信者、時刻、内容、暗号化情報、状態。 |
| `send` | メッセージID、トークID、暗号化情報、グループ鍵登録、連番。 |
| `download` | 保存先パス、バイト数、メッセージID。 |
| `react`、`unsend` | 操作、トークID、メッセージID、連番。 |
| `watch` | 1行に1件のJSONイベント。 |
| `update` | インストール済み／最新バージョン、状態、インストール方法、次の手順。確認だけなら `--check --json`。 |

一部を復号できない場合も、`messages --json` は各メッセージの状態を含む全配列を出力してから、
終了コード1で終了します。暗号化チャンクや生のサーバー応答本文は表示しません。

<a id="json-lookup-results"></a>

### JSONのユーザー検索結果

`contacts --mid MID --json` は、IDが1つでも配列を返します。各レコードには既存の
`mid`、`displayName`、`displayNameOverridden`、`statusMessage`、`picturePath` と、
次のフィールドがあります。

| フィールド | 意味 |
| --- | --- |
| `effectiveDisplayName` | 自分で設定した名前。未設定ならプロフィール名。名前がなければ空です。 |
| `status` | LINEがプロフィールを返した場合は `resolved`、そのMIDを返さなかった場合は `unavailable`。 |

プロフィールがない場合も、要求した `mid` と空の名前・プロフィール欄を持つレコードが残ります。
返されたプロフィールの名前が空の場合もあるため、表示にはMIDを代わりに使えます。
重複した入力IDは1件になるので、`mid` でレコードを対応付けてください。

すべてのプロフィールが取得できれば終了コード0です。取得できないものがあれば全JSON配列を出し、
stderrに件数を報告して、終了コード1で終了します。スクリプトはこのJSONを部分的な結果として利用できます。
ネットワーク、認証、応答形式のエラーでは、先のバッチが成功していても結果配列を出力しません。

<a id="account-and-session"></a>

## アカウントとセッション

<a id="where-your-session-is-stored"></a>

### セッションの保存先

保存できるLINEアカウントは、OSユーザーごとに1つです。

| システム | 保存方法 |
| --- | --- |
| macOS | キーチェーン（Keychain）。 |
| Linux | 暗号化セッションファイル。鍵はSecret Serviceに保存します。 |
| Windows | 現在のユーザーのDPAPIで暗号化したセッションファイル。 |

パスワードはログイン時だけ使い、保存しません。セッション更新中はロックを取得します。
別のコマンドでセッションが使用中と表示された場合は、更新完了後に再試行してください。

LINEに接続せず、ローカルストレージを確認します。

```sh
line auth status
line auth status --check --json
```

`auth status` はローカルストレージへのアクセスを確認します。LINEへの接続やトークン更新は行わず、
保存済みログインが現在も有効かどうかは確認しません。アクセスできない場合は、ログアウト扱いではなく
読み取り不能として報告します。`--check` はロック解除の入力を求めずにできる範囲で書き込み可否を確認します。
macOS KeychainとLinux Secret Serviceでは `interactive_check_required` になる場合があり、
完全な書き込み確認は対話式ログイン中に行います。ヘッドレス環境の再起動後のアクセスは、
実際のホストでテストするまで `expected_not_verified` のままです。

ローカルセッションを削除するには：

```sh
line logout
```

ログアウトしても、LINEサーバー上のセッションは失効しません。

<a id="token-refresh"></a>

### トークンの更新

通常のアクセストークンは約7日で更新の境界に達しますが、保存済みセッションの有効期間が
7日間に固定されているわけではありません。保存済みのリフレッシュトークンが使えれば、
再起動後もCLIが自動でアクセスを更新します。更新が繰り返し失敗する場合や
リフレッシュトークンがない場合は、`line login` が必要になることがあります。

読み取り専用のリクエストは、認証情報を更新して1回再試行できます。
送信、リアクション、送信取消、アップロードは自動再実行しません。
結果が不確かな操作を繰り返す前に、LINEで確認してください。
watcherは必要に応じて保存済み認証情報で再接続します。

保存済みセッションを無効とするのは、サーバーから明示的なログアウト通知があった場合だけです。
トークン期限切れ、一般的なHTTP 401／403、更新の拒否、ネットワーク障害だけでは無効扱いにしません。
ログアウト通知だけでは、別のクライアントに置き換えられたかどうかはわからない場合もあります。
トークン変更後に保存が失敗すると、新しいトークンを保存できた保証がないため、以降のリクエストを停止します。

更新タイミング、再試行のルール、呼び出し経路の根拠は、
[トークンとセッションの監査](../TOKEN_SESSION.md)を参照してください。

<a id="linux-servers-and-headless-storage"></a>

## Linuxサーバーとヘッドレスストレージ

<a id="enroll-without-an-unlocked-keyring"></a>

### ロック解除済みキーリングなしで初期登録する

対応Linuxホストにロック解除済みSecret Serviceキーリングがない場合は、
ヘッドレスストレージを使って対話式で登録します。

```sh
line login --headless --email you@example.com
line auth status
line auth status --check --json
```

この例はメールログインです。`line login --headless` は実験的なQRログインを選び、
同じストレージ保護を使います。登録には対話式ターミナルが必要です。
`--headless` はログインを無人実行にするオプションではありません。

LINEへの接続前に、**Host key; no TPM**（ホスト鍵、TPMなし）という保護方式への同意を求めます。
`--force` でも省略できません。この保存方法はほかの一般ユーザーから保護しますが、
root、自分のアカウントで動くマルウェア、ディスク全体のコピーを持つ人には対抗できません。
TPM保護は提供しません。新しいセッションの保存前にキャンセルすると、新しいローカルセッションは残りません。

登録後はストレージのオプションなしで通常のコマンドを使います。再ログインでも選んだバックエンドを維持します。
`login --headless` は既存のネイティブセッションを変換・上書きしません。

信頼できる `/usr/bin/systemd-creds` ヘルパーと、ユーザーの認証情報ブローカーが必要です。
対話コマンド、SSH、cron、サービスでは同じUnixアカウントと設定ディレクトリを使ってください。
systemd 256〜259を受け付けます。Debian 13／systemd 257とUbuntu 26.04／systemd 259は
使い捨てVMで検証済みです。ほかの対応版もローカルの確認を通過する必要があります。
古い版と未確認の新しい版は拒否します。UniPiや物理TPMでの動作は未検証です。

<a id="move-an-existing-linux-session"></a>

### 既存のLinuxセッションを移行する

アクセス可能なネイティブLinuxセッションをヘッドレスストレージへ移すには：

```sh
line auth migrate --storage=headless
```

移行にはターミナルと、同じホスト鍵のみの保護への明示的な同意が必要です。
watcher、自動化、古いCLIを先に停止してください。トークン、暗号鍵、リクエストの連番、
watchのチェックポイントを保持し、LINEへの接続やパスワード入力は行いません。

途中で止まった場合は同じコマンドを再実行してください。`migration.pending` の削除や
`session.enc` の手動置き換えはしないでください。後処理が完了するまで、
`line auth status` は `migration_pending` を報告します。
復旧手順は[ヘッドレスストレージの内部仕様](../internal/session/HEADLESS.md)を参照してください。

<a id="run-a-watcher-without-a-login-session"></a>

### OSへのログインなしでwatcherを動かす

専用の一般ユーザーで対話式の初期登録を行います。再起動後、サービスで使うものと同じユーザー・環境で
`line auth status --check` を実行してください。`HOME` と `XDG_CONFIG_HOME` は統一します。
サービスやcronジョブの導入・管理は利用者が行い、CLIは作成しません。

systemdのユーザーサービスでは、登録したアカウントの
`~/.config/systemd/user/line-watch.service` に次の例を保存します。
実行ファイルの場所が違う場合は `ExecStart` を変更してください。`%h` はホームディレクトリです。
ユーザーサービスに `User=` は追加しないでください。

```ini
[Unit]
Description=LINE event watcher
StartLimitIntervalSec=300
StartLimitBurst=5

[Service]
Environment=HOME=%h
Environment=XDG_CONFIG_HOME=%h/.config
UMask=0077
NoNewPrivileges=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
LockPersonality=true
MemoryDenyWriteExecute=true
ExecStart=/usr/local/bin/line watch
Restart=on-failure
RestartSec=15s
RestartPreventExitStatus=65 69 74 78

[Install]
WantedBy=default.target
```

この設定は、[issue #3](https://github.com/kongesque/line-cli/issues/3)で報告された
Debian 13／systemd 257.13 ARM64環境で動作しました。有効化前に自分のホストでも確認してください。
`AF_UNIX` は認証情報ブローカーに、`AF_INET` と `AF_INET6` はLINEへの接続とDNSに必要です。

```sh
systemctl --user daemon-reload
systemctl --user start line-watch.service
systemctl --user status line-watch.service
# ストレージとwatcherの動作を確認してから有効化：
systemctl --user enable line-watch.service
```

対話ログインなしでユーザーサービスを起動するには、管理者によるlingeringの有効化が必要な場合があります。
システムサービスにする場合は `User=linebot` を追加し、両方の環境変数に明示的な `/home/linebot` の
パスを使い、`WantedBy=multi-user.target` に設定します。

watchの出力には非公開のメッセージが含まれます。ジャーナルや出力ファイルへのアクセスを制限してください。
cronでも同じアカウントとパスを使い、`umask 077` を設定します。

<a id="diagnose-a-headless-service"></a>

### ヘッドレスサービスの問題を調べる

[issue #3](https://github.com/kongesque/line-cli/issues/3)のユーザーサービス環境では、
次のsystemd設定で失敗しました。

| 設定 | 観測結果 |
| --- | --- |
| `PrivateTmp=true` | ヘッドレスヘルパーを利用できず、終了コード69。 |
| `ProtectSystem=strict` | ヘッドレスヘルパーを利用できず、終了コード69。 |
| `ProtectHome=read-only` | ファイル書き込みの失敗など、非ゼロで終了。 |

基本のユニットには、これらと `PrivateUsers=` を入れないでください。
`ReadWritePaths=%h/.config/line-cli` の追加だけでは、報告された問題は解消しませんでした。
これは1つのユーザーサービス環境での観測であり、すべてのシステムサービスに共通するルールではありません。

systemdが作るユーザー名前空間により、ユーザーサービスからホストのroot UIDが見えなくなる場合があります。
`PrivateUsers=true` では、root所有のヘルパーの所有者が未マッピングに見えることがあります
（[systemd 257のドキュメント](https://github.com/systemd/systemd/blob/v257/man/systemd.exec.xml)）。
所有者と権限を検証できないヘルパーは拒否します。読み取り専用マウントは、ロック、パス記録、
更新後のトークン、リクエストの連番、watchチェックポイントの書き込みも妨げます。
`$XDG_CONFIG_HOME/line-cli` と `$HOME/.config/line-cli` は、別のディレクトリでも両方に書き込みが必要です。

サービスユーザーで `line auth status --check --json` を実行します。
同じ環境・保護設定を使い、対話実行と一時的なユニットでの結果を比較してください。
例えば次のように実行できます。必要なら実行ファイルのパスを変更します。

```sh
systemd-run --user --wait --collect \
  --property=Environment=HOME="$HOME" \
  --property=Environment=XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-$HOME/.config}" \
  --property=UMask=0077 \
  --property=NoNewPrivileges=true \
  --property='RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6' \
  --property=LockPersonality=true \
  --property=MemoryDenyWriteExecute=true \
  /usr/local/bin/line auth status --check --json
```

これはローカルの確認で、LINEには接続しません。サンドボックス設定は1つずつ変更してください。

- `headless_helper_untrusted`（終了コード69）：ヘルパーまたは親ディレクトリの所有者・権限検証に失敗。
  エラーメッセージにあるサンドボックス設定と、実際のインストール先の権限を確認します。
- `headless_unavailable`：検出、バージョン対応、またはrootとしての実行に失敗。
  エラーメッセージに失敗した段階が表示されます。
- `storage_unavailable`：認証情報ブローカーや封印解除に失敗した可能性があります。
  ヘルパーのstderrは表示しません。

再登録や削除を考える前に、動作するサービス環境へ戻してください。サービスが停止した場合は、
ストレージを修復し、`systemctl --user reset-failed line-watch.service` を実行してから再起動します。
終了コード69ではwatcherの自動再起動を抑止します。この再起動設定を送信やほかのリモート変更に適用することはありません。

<a id="linux-session-files-and-upgrades"></a>

### Linuxのセッションファイルとアップグレード

Linuxでは暗号化セッションファイルと2つのプロセスロックを `$XDG_CONFIG_HOME/line-cli`
（通常は `~/.config/line-cli`）に保存します。`XDG_CACHE_HOME` を変えてもロックの場所は変わりません。
アプリのディレクトリは `0700`、ファイルは `0600` にしてください。
安全でない所有者やファイル種類、アプリディレクトリ・ファイルのシンボリックリンク、
ハードリンクされたファイルは拒否します。既存の親ディレクトリの権限を自動変更することはありません。

ログインは入力を求める前にストレージを確認し、LINE接続前にセッションと保存先の識別情報を再確認します。
別の一時的な認証情報項目や暗号化ファイルを使い、現在のセッションは残します。
読めない、または破損したストレージは、先にアクセスを復旧するか、明示的にログアウトして
ローカルセッションを削除してください。入力中に別のログイン・ログアウトがセッションを変更した場合は、
コマンドを最初から実行してください。

Linuxでは、確認したセッションのパスを `$HOME/.config/line-cli` の `native-paths.json` にも記録します。
古い版で別の `XDG_CONFIG_HOME` を使った場合は、移行・ログアウト前に各旧保存先で
`line auth status` を1回ずつ実行してください。これにより、ほかの既知のディレクトリに必要な鍵の
削除を避けられます。手順を省くためにパス記録やロックファイルを削除しないでください。

このロック配置へ更新する前に、古いコマンドとwatcherを停止してください。新旧バイナリの同時実行は非対応です。
ログアウト後もロックファイルは残します。ネイティブLinuxストレージはSecret Serviceキーリングごとに
1つの鍵識別子を使うため、設定ディレクトリを分けて複数アカウントを使う方法には対応していません。

永続保存の成否が不確かなエラーでは、ファイルがすでに変わっている可能性があります。
同じローカル操作を繰り返し、古いコピーは復元しないでください。
ログアウトには非公開の復旧記録があり、再実行して中断した後処理を完了できます。

<a id="storage-exit-codes"></a>

### ストレージの終了コード

| コード | 意味 |
| --- | --- |
| 65 | 不正な形式、鍵の不足、認証失敗、保護方式の不一致。 |
| 69 | ストレージやヘルパーが利用不能、またはタイムアウト。 |
| 74 | 永続保存の成否が不確か、または確認用データの削除に失敗。 |
| 75 | ローカルの競合、ログイン中の保存先変更、ヘルパーのキャンセル。 |
| 78 | 設定、同意、移行、修復、対話式の確認が必要。 |

シグナルが優先され、SIGINTは130、SIGTERMは143です。その他のCLI・ネットワークエラーは1です。
ストレージが利用できなくても、`auth status --json` は状態オブジェクトを出力してから
対応する非ゼロのコードを返す場合があります。

<a id="reference"></a>

## リファレンス

<a id="command-help"></a>

### コマンドのヘルプ

最新のオプション一覧は組み込みヘルプで確認できます。

```sh
line help
line login --help
line contacts --help
line chats --help
line messages --help
line send --help
line watch --help
line update --help
```

<a id="build-from-source"></a>

### ソースからビルドする

GitとGo 1.26以降が必要です。macOSではKeychain対応のため、Xcode Command Line ToolsとCGOも必要です。

macOSまたはLinux：

```sh
git clone https://github.com/kongesque/line-cli.git
cd line-cli
./install.sh
```

Windows PowerShell：

```powershell
git clone https://github.com/kongesque/line-cli.git
Set-Location line-cli
$env:CGO_ENABLED = "0"
go build -trimpath -o .\bin\line.exe ./cmd/line
.\bin\line.exe help
```

コントリビューター向けのチェック：

```sh
go test -race ./internal/... ./cmd/line ./pkg/line/... ./pkg/e2ee
go vet ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
go build -trimpath -o bin/line ./cmd/line
```

通常のテストは偽のAPIと認証情報を使います。実接続テストは初期状態では無効で、明示的な許可が必要です。
キャンセルのテストには合成の子プロセスを使います。macOS／Linuxのパスワード入力テストは、
利用可能であればPython 3標準ライブラリのPTY機能を使います。
ネイティブLinux Secret Serviceの統合テストには、明示的に有効化した使い捨てD-Busセッションが必要です。
Windows DPAPIのテストには一時ファイルを使います。

<a id="current-limitations"></a>

### 現在の制限

- 保存できるLINEアカウントはOSユーザーごとに1つです。
- QRログインは実験的で、Letter Sealingを無効にしたアカウントには対応しません。
- 履歴は直近のものに限られ、1回の取得は最大100件です。
- 一般ファイルは送信できますが、スタンプや専用のメディア送信には対応しません。
- 画像・動画・音声・ファイルのダウンロードは20 MiBまで。外部メディアURLには対応しません。
- メッセージを読んでも既読にはなりません。
- リリース版バイナリは署名・公証されていません。
- LINEのプロトコル変更で互換性に影響が出る場合があります。

LINE CLIは `beeper/line` を基にした独立プロジェクトです。LINEの公式製品ではなく、
BeeperやMatrixホームサーバーも必要ありません。
