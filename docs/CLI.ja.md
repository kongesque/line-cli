# LINE CLIガイド

[English](CLI.md) | [繁體中文（台灣）](CLI.zh-TW.md) | 日本語 | [ภาษาไทย](CLI.th.md)

`main` のガイドは開発ブランチの説明です。インストール済みリリースについては `line version`、
`line COMMAND --help` と該当タグのガイドを確認してください。[変更履歴](../CHANGELOG.md)も参照してください。

[はじめに](#get-started) · [日常のコマンド](#daily-commands) · [更新](#updating-line-cli) · [スクリプト](#use-from-scripts) · [アカウント](#account-and-session) · [Linux / ヘッドレス](#linux-servers-and-headless-storage) · [問題の解決](#troubleshooting) · [リファレンス](#reference)

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

DebianやUbuntuのデスクトップでは、ログイン前に認証情報の保存用ツールを導入します。

```sh
sudo apt install libsecret-tools gnome-keyring
```

デスクトップのキーリングは、`line` と同じD-Busセッションで動作し、ロックが解除されている必要があります。
キーリングのないサーバーやSSH環境では `line login --headless` を使います。
[ヘッドレス環境の設定](#linux-servers-and-headless-storage)を参照してください。

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

環境に合うログイン方法を1つ選びます。

| 環境 | コマンド |
| --- | --- |
| macOS、Windows、またはキーリングをロック解除したLinuxデスクトップ | `line login` |
| デスクトップのキーリングがないLinuxサーバーやSSH環境 | `line login --headless` |

スマートフォンのLINEのスキャナーでQRコードを読み取り、ログインを承認します。
PINを求められたら、先頭のゼロも含めて入力し、**Session saved securely** と表示されるまで待ちます。
QRログインは実験的な機能で、利用可能なLetter Sealingキーが必要です。
ヘッドレス環境の要件は[Linuxサーバーの設定](#linux-servers-and-headless-storage)を参照してください。

次にアカウントを確認し、対話式の案内に従ってトークを選びます。

```sh
line whoami
line chats
line messages
line send
```

`line send` では送信先を選び、本文を入力してEnterキーを押すと送信します。
メールアドレスとパスワードを使う場合は、下記の方法を選んでください。パスワードは画面に表示せず、保存もしません。
パスワードをオプションや環境変数で渡すことはできません。

<a id="login-options-and-qr-help"></a>

### ログインのオプションとQRコードのトラブルシューティング

| コマンド | 用途 |
| --- | --- |
| `line login --qr` | デフォルトのQRログインを明示的に選択します。 |
| `line login --email ADDRESS` | LINEアカウントに設定済みのメールアドレス・パスワードとスマートフォンの認証を使います。 |
| `line login --headless --email ADDRESS` | Linuxのヘッドレスストレージでメールログインします。 |
| `line login --qr-url` | 一度限りのQRデータを信頼できるローカルツールに渡します。 |
| `line login --force` | 保存済みセッションを置き換える前の確認を省略します。 |

`--email` と `--qr`／`--qr-url` は併用できません。どちらのQR方式も対話入力できるターミナルが必要です。
stdoutはリダイレクトできます。進行状況、QRコード、PIN、警告はstderr、最後の成功メッセージはstdoutに出力します。

- **QRコードが収まらない：** 必要な幅と `--qr-url` の案内を表示し、コードは切り取りません。
  `NO_COLOR` は明示的な色指定を無効にします。
- **QRデータの扱い：** `--qr-url` は信頼できるローカルツールだけで使い、共有・アップロード・保存しないでください。
  値はスクロール履歴に残りますが、QR画像は保存しません。データを書き出す可能性がある `QRCODE_DEBUG` は解除してください。
- **有効期限：** LINEが期限切れを確認すると新しいコードを表示し、1回のログインで最大3個まで使えます。
  通信エラーやPINのタイムアウトでは再試行しません。残り時間は目安で、簡易ターミナルやリダイレクトしたstderrには状態だけを表示します。
- **証明書エラー：** 保存済みQR証明書が拒否されてもPIN認証には切り替えません。不明な証明書エラーでもログインを停止します。
  設定済みならメールログインを試してください。`Diagnostic: verifyCertificate` は数値コードのみで、
  QR、PIN、証明書、トークン、サーバーの応答本文は含みません。

<a id="saved-sessions-and-cancellation"></a>

### 保存済みセッションとキャンセル

ログインはLINEへの接続前にローカルストレージを確認し、利用可能な保存済みセッションを置き換える前に確認します。
**no** と答えると保持します。`--force` が省略するのはこの確認だけです。
ストレージ検査、ヘッドレスストレージへの同意、セッションロック、プロトコルの検査は省略しません。

| `--timeout DURATION` に対応するコマンド | デフォルトの制限時間 |
| --- | --- |
| `download` | `2m` |
| `whoami`、`contacts`、`chats`、`members`、`messages`、`send`、`react`、`unsend` | `0`（コマンド全体の制限なし） |

例：`line send "Alice" --text "Hello" --timeout 30s`。オプション解析後から計測し、入力待ち、stdin、
トークやメッセージの検索、認証の復旧、キーの準備、転送を含みます。
`--timeout 0` で無効にしても、個別のHTTPタイムアウトは有効です。期限切れは通常、終了コード1になります。

Ctrl-Cで入力待ち、QRのポーリング、通常のリクエストを中止します。SIGINTは130、SIGTERMは143で終了します。
終了前にパスワード入力のエコー設定を戻します。従来のメール認証はHTTPタイムアウトに従います。
ローカルの暗号処理とファイル書き込みは、現在の処理を終えてからキャンセルを確認します。

送信済みのリクエストは、中断時点で成功している場合があります。繰り返す前にLINEで確認してください。自動再送はしません。
後続のアップロードや出力が失敗しても、確認済みの送信結果と作成済みの非E2EEのファイルメッセージIDは保持します。
成功結果とシグナルによる終了コードが同時に返る場合があります。

新しいセッションの保存前に中止すると、古いローカルセッションは残りますが、LINE側のChrome形式のセッションは置き換わっている場合があります。
ログインを自動再試行することはありません。保存結果が不確かな場合は `line auth status --check` で確認し、
古いコピーの復元や確認せずに再ログインすることは避けてください。保存完了後にシグナルが届いても、成功を報告します。

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
`q` またはCtrl-Cでキャンセルできます。`--json`、`--error-format=json`、`--stdin` は対話入力を無効にするため、
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

`messages` は既定で直近20件を取得します。`--limit` は1〜100件を指定できます。
通常の表示は古い順、通常の `--json` はサーバーの順序です。
`--show-ids` で返信、リアクション、ダウンロード、送信取消に必要なIDを表示します。
閲覧しても既読にはならず、本文をローカルに保存することもありません。
元の端末の鍵をLINEから取得できない場合、古い暗号化メッセージの一部は読めません。

本文を表示せず、復号や鍵の取得も行わずに、返されたIDを調べるには：

```sh
line --error-format=json messages "Chat name" --diagnostics --json --limit 50
```

`--diagnostics` には `--json` が必要で、`--details`、`--resolve-names`、`--page`、`--cursor` とは併用できません。
IDがないことは、この応答に含まれていないことだけを意味します。[診断フィールド](JSON.md#history-diagnostics)を参照してください。

<a id="history-pagination-json"></a>

### 履歴のページ分割JSON

```sh
line messages CHAT_ID --page --json --limit 50
line messages CHAT_ID --page --json --limit 50 --cursor 'NEXT_CURSOR'
```

`--page` には `--json` が必要で、`--cursor` には両方が必要です。1回につき1ページを取得し、
`schema_version: 1`、`chat_id`、`order: "newest_first"`、`messages`、
`exhausted`、`stop_reason`、任意の `next_cursor` を返します。IDは文字列のまま、数値として新しい順に並びます。
カーソルは同じトークへそのまま渡してください。正確な配信時刻とメッセージIDを保持し、作成時刻で代用しません。
不正なカーソルや別トークのカーソルは履歴取得前に拒否します。トーク名を指定した場合は、先にIDへ解決します。

`stop_reason` は指定件数に達すると `count_limit`、短い非空ページでは `page_limit` です。
重複アンカーを除いて空の場合だけ `end_of_available_history`、`exhausted: true` を返し、`next_cursor` を省略します。
短いページでも末尾とは限りません。100件の指定でもアンカーのため99件になることがあり、次のページが空になる場合もあります。
取得できるのは、このクライアントにまだ提供される履歴だけです。完全な履歴や固定の保存期間は保証しません。

配信時刻の欠落、不正なページ、先に進まないページはエラーになります。`--timeout` は取得とデコードを含みます。
タイムアウトやキャンセルを履歴の末尾として扱うことはありません。一部の復号に失敗してもページとカーソルを出力し、非ゼロで終了します。
続行前に各メッセージの状態を確認してください。通常の `--json` は引き続き配列を返します。

<a id="detailed-content-and-chat-json"></a>

### 詳細コンテンツ JSON

```sh
line chats --details --json
line messages CHAT_ID --details --json --resolve-names
line messages CHAT_ID --details --page --json --limit 50
```

`--details` は `--json` と併用し、デコードした内容と不明な値を明示する `schema_version: 1` のオブジェクトを返します。
`--resolve-names` を追加すると送信者名を取得します。既存のJSONとwatch形式は変わりません。
一部の復号に失敗しても結果を出力し、非ゼロで終了します。
フィールド、状態、制限は[詳細出力仕様](JSON.md#detailed-content)を参照してください。

<a id="list-group-members-and-mention-someone"></a>

### グループメンバーの確認とメンション

```sh
line members "Family group" --json
line members "Family group" --timeout 30s
line send "Family group" --text '😀 @Alex' --mentions '[{"start":3,"end":8,"mid":"USER_MID"}]'
line send "Family group" --text '@All hello' --mentions '[{"start":0,"end":4,"all":true}]'
```

`USER_MID` は `members` が返す完全なユーザー MID に置き換えてください。
`members` はグループ ID または一意に一致する名前、`--timeout`、グローバルな
JSON エラー形式に対応します。個別トークと旧形式の複数人トークには未対応です。
暗号鍵の取得・登録や既読通知は行いません。

JSON は `schema_version:1`、`chat`、`members`、`invitees`、
`members_complete`、`invitees_complete` を含みます。現在のメンバーと招待中の
ユーザーを別々に MID 順で返します。各項目は `mid`、表示に使用する名前 `name`
（不明なら `null`）、`name_status`（`resolved` または `unavailable`）、`self`
を持ちます。明示的な空リストは完全、省略または `null` は不完全です。
プロフィール取得に失敗しても MID は保持し、自分自身を勝手に追加しません。
完全性は取得したメンバーリストを表し、名前の取得状況や将来の変更を保証しません。

`--mentions` は最大 100 件の JSON オブジェクトを受け取ります。`start` は開始位置を
含み、`end` は終了位置を含みません。実際の本文の UTF-16 単位で指定するため、
`😀` は 2 単位です。各項目に完全な `mid` または `all:true` を指定し、範囲の
重複やサロゲートペアの分割はできません。単なる `@名前` はメンションになりません。
全員へのメンションはグループまたは複数人トークに限定されます。`--text`、
`--stdin`、`--reply-to` と併用でき、`--file`、`--image` や対話式の本文入力とは併用できません。
入力はセッションへのアクセス前に検証します。暗号化と一度だけ送信する規則は
維持され、メンション情報は Chrome と同様に暗号化本文の外に置かれます。

スクリプトでは、stdinの本文を変更せず、その内容に合う位置を指定してください。

```sh
printf '%s' '😀 @Alex' | line send "Family group" --stdin --reply-to MESSAGE_ID --mentions '[{"start":3,"end":8,"mid":"USER_MID"}]' --json
```

<a id="send-messages-and-files"></a>

### メッセージやファイルを送信する

```sh
line send "Alice" --text "Hello!"
line send "Alice" --stdin < message.txt
line send "Alice" --text "Sounds good" --reply-to MESSAGE_ID
line send "Family group" --file ./report.pdf
line send "Family group" --file ./report.pdf --reply-to MESSAGE_ID
```

`--text`、`--stdin`、`--file`、`--image` のいずれか1つを指定します。テキストは空にできず、
上限はUTF-16で10,000コード単位です。ファイルは20 MiBまでです。`--file` で送る画像・動画・音声は
通常のファイルとして表示され、元のバイト列を保持します。

**暗号化：** 送信はLetter Sealingを優先します。LINEがE2EE非対応と明示した場合は、確認なしでHTTPS/TLSによる送信を行います。
転送中のデータは暗号化されますが、LINEは非E2EEの内容を読めます。スクリプト、`--json`、`--error-format=json`、`--stdin` でも確認しません。

Letter Sealingで送信できる場合だけ送るには、次を指定します。

```sh
line send CHAT_ID --file ./report.pdf --require-e2ee --json
```

非対応の場合はリモート変更前に `encryption_required` で停止します。
[LINE暗号化レポート2025（英語）](https://www.lycorp.co.jp/en/privacy-security/security/transparency/encryption-report/2025/)はTLS、Letter Sealing、新しい公式クライアントのメディアE2EE対応を説明しています。
本CLIのメディア相互運用性は別途検証が必要です。ファイルとテキストでは暗号化への対応を個別に確認します。
対応情報の欠落や不正、鍵の不足、不完全なメンバー情報、通信障害を理由にE2EEを無効にすることはありません。
新しいグループ鍵の登録は、明示的な送信で現在の全メンバーを把握できている場合に限ります。

**画像メッセージ：** 本体とプレビューで暗号化ストリームが再利用されるため、暗号化した `--image` 送信は
無効です。アップロード、グループ鍵登録、メッセージ作成の前に停止し、非E2EEへの自動切り替えでも回避できません。
元の画像を一般ファイルとして送るには `--file` を使います。ファイルの対応状況に関する上記の確認は必要です。

LINEが非E2EEの画像送信に対応すると明示した場合は、次を使います。HTTPS/TLSで転送しますが、LINEは画像を読めます。

```sh
line send CHAT_ID --image ./screenshot.png --json
```

`--image` は実際のJPEGまたは静止PNGデータを受け付けます。上限は20 MiB、4,000万画素、各辺16384画素です。
セッションへのアクセス前にEXIFの向きを適用し、メタデータを除去し、透明部分を白にして、縦横比を保ちつつ
総画素数1280×1280以下、品質60のJPEGへ変換します。GIF、アニメーションPNG、BMP、WebP、動画・音声の専用送信、
画像による返信は非対応です。本文、stdin、file、mentionsとの併用もできません。画像送信の実環境での相互運用性は
未検証です。[プロトコルの根拠](protocol/endpoints/image-send.md)を参照してください。

`--json` にはメッセージID、暗号化状態、グループ鍵登録、リクエスト連番を出力します。
通常の送信結果には `end-to-end encrypted` または `transport encrypted (no E2EE)` と表示します。
送信結果の `encrypted: false` はLetter Sealing未使用を示し、HTTPS/TLSによる通信の暗号化は引き続き有効です。

> [!CAUTION]
> 送信、アップロード、その他のリモート変更は各1回だけ試行します。非E2EEのファイルメッセージはアップロード失敗前に
> 作成される場合があり、構造化エラーには確認済みIDが残ります。結果が不明な場合は再試行前にLINEを確認してください。

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

ダウンロード、リアクション、送信取消、`send --reply-to` は選択したトークだけを検索します。
認証の復旧を含め、最大10ページ、各100件、30秒です。短いコマンド期限が優先されます。
すべての対象についてトークとの一致を確認し、送信取消では所有者も確認します。
返信は対象が見つかってから、アップロード、グループ鍵登録、送信を行います。
検索範囲外の対象はローカルで拒否します。全トークを横断するID検索ではありません。

空の過去ページでは `message_not_found`、ページ数上限では `lookup_incomplete`、時間切れでは `deadline_exceeded` を返します。
どの結果も、メッセージが一度も存在しなかったことを意味しません。対話式の選択肢は直近100件だけです。
古いメッセージを探すにはIDを直接指定してください。ダウンロードは保存前に暗号化データを検証し、既存ファイルを上書きしません。
リモートのメタデータが保存先を決めることもありません。

`download` は画像・動画・音声・一般ファイルのメッセージに対応し、上限は20 MiB、
コマンド全体の期限は既定で2分です（`--timeout` で変更可能）。メディアの全バイトをそのまま保存するため、拡張子を変えても形式は変換されません。
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

スクリプトでは完全なトークIDを使い、メッセージIDは文字列のまま扱ってください。
`--json`、`--error-format=json`、`--stdin` は対話入力を無効にするため、必要な引数をすべて指定します。JSONを出力するコマンドの例：

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

<a id="structured-errors"></a>

### 構造化エラー

`line --error-format=json COMMAND ... --json` を使うと、成功結果と部分結果のJSONをstdoutに保ったまま、
stderrに構造化エラーを1件出力します。グローバルオプションはコマンドの前に指定し、対話入力を無効にします。
基本コマンドと `auth status` に対応します。ログイン、ログアウト、移行、watch、updateでは処理前に拒否されるため、通常の診断出力を使ってください。

エラーには安定したコードと処理段階を含みます。メッセージ操作では、既知のトーク／メッセージID、リクエスト連番、
操作を試行したかどうか、結果、準備やアップロードの各段階も報告します。アップロードが失敗しても、ファイルメッセージ自体は作成済みの場合があります。
結果の出力に失敗しても、送信済みという結果は保持します。通信エラーを含め、`unknown` の操作を自動再試行しないでください。
先にLINEで確認してください。ストレージとシグナルの終了コードは変わりません。[エラーのフィールド、コード、例](JSON.md#errors)を参照してください。

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
[トークンとセッションの監査](SESSION.md#token-refresh)を参照してください。

<a id="linux-servers-and-headless-storage"></a>

## Linuxサーバーとヘッドレスストレージ

<a id="enroll-without-an-unlocked-keyring"></a>

### キーリングなしで初期設定する

通常のUnixユーザーと対話式ターミナルを使います。SSH接続には `ssh -t user@host` を使えます。
信頼できる `/usr/bin/systemd-creds`（systemd 256–259）と、動作するユーザー認証情報ブローカーが必要です。
古いバージョンや未確認の新しいバージョンは拒否します。

```sh
line login --headless
line auth status --check --json
```

最初のコマンドは実験的なQRログインを使います。メールアドレスとパスワードを使う場合は、
`line login --headless --email you@example.com` に置き換えます。
**Session saved securely** と表示されてからストレージを確認してください。
`--headless` は保存方法を選ぶオプションで、初回ログインには対話操作とスマートフォンでの承認が必要です。

LINEへの接続前に **Host key; no TPM** への同意を求めます。`--force` でも省略しません。
この方式は他の非特権ユーザーから保護しますが、root、自分のアカウントで動くマルウェア、ディスク全体のコピーには対応しません。
TPM保護はありません。保存前に中止した場合、新しいローカルセッションは残りません。

初期設定後の通常コマンドに `--headless` は不要です。再ログイン時は選択済みの保存方式を使います。
`login --headless` は既存のネイティブセッションを変換・上書きしません。
コマンド、SSH、cron、サービスで同じUnixユーザーと設定ディレクトリを使ってください。

Debian 13/systemd 257とUbuntu 26.04/systemd 259では、使い捨てVMで検証済みです。
他の対応バージョンもローカルでの検査が必要です。UniPiと物理TPMの動作は未確認です。

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
復旧手順は[ヘッドレスストレージの内部仕様](SESSION.md#headless-storage-engine)を参照してください。

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
ExecStart=%h/.local/bin/line watch
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
  "$HOME/.local/bin/line" auth status --check --json
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

<a id="troubleshooting"></a>

## 問題の解決

| 症状 | 次に確認すること |
| --- | --- |
| ガイドのオプションが不明と表示される | `line version` と `line COMMAND --help` を実行し、該当タグのガイドと比較します。更新確認は `line update --check` です。 |
| `encryption_required` | E2EE非対応のため厳格モードで停止しました。`--require-e2ee`を外すとHTTPS/TLSで送信しますが、LINEは内容を読めます。 |
| `encrypted_image_unavailable` | 暗号化画像は無効です。元のバイト列には `--file` を使いますが、ファイルの対応状況の確認は必要です。 |
| SSHやサービスでストレージにアクセスできない | 同じユーザーと環境で `line auth status --check --json` を実行し、[サービスの診断](#diagnose-a-headless-service)を確認します。回避のためにセッションファイルを削除しないでください。 |
| 送信失敗、または最近の履歴に見つからない | 再実行前にLINEを確認します。[構造化エラー](#structured-errors)で確認済みか不明かを判断します。最近の履歴にないことは未配信の証拠ではありません。 |
| 履歴ページが短い、または一部を復号できない | `exhausted` が真になるまでカーソルで続行できます。各状態と終了コードを確認してください。古い履歴や鍵は取得できない場合があります。 |

不具合報告には `line version`、OS、コマンド、機密情報を除いたエラーを含めてください。
トークン、パスワード、QR/PIN、鍵、私的なメッセージは含めないでください。

<a id="reference"></a>

## リファレンス

<a id="command-help"></a>

### コマンドのヘルプ

最新のオプション一覧は組み込みヘルプで確認できます。

```sh
line help
line send --help
```

<a id="build-from-source"></a>

### ソースからビルドする

GitとGo 1.27.3が必要です。macOSではKeychain対応のため、Xcode Command Line ToolsとCGOも必要です。

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

開発用チェック、テストデータ、プラットフォーム統合、リリース手順は
[開発ガイド](DEVELOPMENT.md)を参照してください。通常のテストは合成データと偽のAPIを使います。
実際のLINEを使うテストには明示的な許可が必要です。

<a id="current-limitations"></a>

### 現在の制限

- LINEがE2EE非対応と明示した場合はHTTPS/TLSで送信しますが、LINEは内容を読めます。
  `--require-e2ee` でこの送信を防げます。詳しくは[LINE暗号化レポート2025（英語）](https://www.lycorp.co.jp/en/privacy-security/security/transparency/encryption-report/2025/)を参照してください。
- 保存できるLINEアカウントはOSユーザーごとに1つです。
- QRログインは実験的な機能で、利用可能なLetter Sealingキーが必要です。
- 履歴は1ページ最大100件です。`--page --json` とカーソルで取得可能な古い履歴へ進めます。操作対象の検索は最大10ページ、30秒です。
- 一般ファイルはLINEの対応状況を確認し、非E2EEの場合もHTTPS/TLSで送信します。暗号化画像は無効です。非E2EEのJPEG/PNG画像はLINEが対応を明示した場合だけ送信できます。動画・音声の専用送信、アニメーション画像、画像返信、スタンプ送信は非対応です。
- 画像・動画・音声・ファイルのダウンロードは20 MiBまで。外部メディアURLには対応しません。
- メッセージを読んでも既読にはなりません。
- リリース版バイナリは署名・公証されていません。
- LINEのプロトコル変更で互換性に影響が出る場合があります。

LINE CLIは `beeper/line` を基にした独立プロジェクトです。LINEの公式製品ではなく、
BeeperやMatrixホームサーバーも必要ありません。
