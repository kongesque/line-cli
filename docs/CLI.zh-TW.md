# LINE CLI 使用指南

[English](CLI.md) | 繁體中文（台灣） | [日本語](CLI.ja.md) | [ภาษาไทย](CLI.th.md)

在終端機使用你的個人 LINE 帳號：讀取聊天內容、傳送訊息與檔案、下載附件，以及接收新事件。
日常操作可跟著互動式提示選擇；寫指令稿時則可使用 JSON 輸出。

> [!IMPORTANT]
> LINE 同時只允許一個 Chrome 類型的工作階段。使用 CLI 登入可能會取代現有的
> LINE Chrome 擴充功能或其他 Chrome 類型用戶端的工作階段。

<a id="get-started"></a>

## 開始使用

<a id="install"></a>

### 安裝

macOS 或 Linux：

```sh
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.sh | sh
```

安裝程式會選擇適合的發行檔、驗證檢查碼，將 `line` 安裝至 `~/.local/bin`，
並為常見的 shell 設定 `PATH`。若出現提示，請重新開啟終端機。

macOS 也可使用 Homebrew：

```sh
brew install kongesque/tap/line-cli
```

Debian 或 Ubuntu 使用者請在登入前安裝憑證儲存工具：

```sh
sudo apt install libsecret-tools gnome-keyring
```

Linux 金鑰圈必須已啟動並解鎖，且與 `line` 位於同一個 D-Bus 工作階段。
若透過 SSH 操作，或伺服器沒有已解鎖的金鑰圈，請參閱
[Linux 伺服器與無頭環境儲存](#linux-servers-and-headless-storage)。

Windows 請在 PowerShell 執行：

```powershell
irm https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.ps1 | iex
```

安裝程式會將 `line.exe` 放在 `%LOCALAPPDATA%\line-cli\bin`，並把該目錄加入
使用者的 `PATH`。必要時請重新開啟 PowerShell。

發行版執行檔目前尚未經過數位簽署。你可以到
[GitHub Releases](https://github.com/kongesque/line-cli/releases/latest) 查看或下載。

確認安裝結果：

```sh
line version
line help
```

<a id="sign-in-and-send-your-first-message"></a>

### 登入並傳送第一則訊息

```sh
line login
line whoami
line chats
line messages "Family group"
line send "Alice" --text "Hello!"
```

`line login` 會顯示 QR 碼。使用手機上的 LINE 掃描、核准登入，並依提示輸入
畫面上的 PIN。請等到出現 **Session saved securely** 再執行其他指令。

QR 碼登入目前為實驗性功能。如果你的 LINE 帳號已設定電子郵件與密碼，也可以使用：

```sh
line login --email you@example.com
```

輸入密碼時不會顯示內容，CLI 也不會儲存密碼。沒有密碼選項或密碼環境變數。

<a id="login-options-and-qr-help"></a>

### 登入選項與 QR 碼疑難排解

| 指令或選項 | 適用情境 |
| --- | --- |
| `line login` 或 `line login --qr` | 使用終端機上的 QR 碼登入。 |
| `line login --email ADDRESS` | 改用電子郵件、密碼與手機驗證登入。 |
| `line login --qr-url` | 將一次性 QR 碼內容交給可信任的本機 QR 工具。 |
| `line login --force` | 略過是否取代已儲存工作階段的詢問。 |
| `line login --headless` | 在支援的 Linux 主機上首次設定，且沒有已解鎖的金鑰圈。 |

`--email` 不能與 `--qr` 或 `--qr-url` 同時使用。兩種 QR 登入方式都需要可互動輸入的
終端機，但 stdout 可以重新導向。進度、QR 碼、PIN 與警告寫入 stderr；最後的成功訊息寫入 stdout。

請使用 LINE 內建的掃描器。PIN 必須照畫面完整輸入，包括開頭的零。
手機核准登入，不代表 CLI 已完成個人資料檢查、金鑰匯出與工作階段儲存。

若終端機太窄，CLI 會告知所需寬度，並建議改用 `--qr-url`，不會顯示被裁切的 QR 碼。
`NO_COLOR` 可停用明確指定的色彩。`--qr-url` 輸出的內容屬於機密：只交給可信任的本機工具，
不要分享、送到線上 QR 碼產生器或存檔。內容仍會留在終端機的捲動歷史中。QR 碼圖片不會存檔。
一般 QR 登入前，請先取消設定 `QRCODE_DEBUG`，因為編碼器的除錯模式可能會寫出資料。

倒數時間僅供參考。LINE 確認 QR 碼過期後，CLI 可以顯示新的 QR 碼，每次登入最多三個。
網路錯誤或 PIN 驗證逾時不會觸發重試。一般純文字終端機或重新導向的 stderr
只會顯示狀態變化，不會即時倒數。

QR 登入目前不支援停用 Letter Sealing 的帳號。已儲存的 QR 憑證遭拒時，不會改用 PIN 驗證；
未知的憑證錯誤也會中止登入。若遇到憑證驗證錯誤，請試用電子郵件登入。
若訊息帶有 `Diagnostic: verifyCertificate` 後綴，其中只有數字代碼，
不會包含 QR 碼、PIN、憑證、權杖或伺服器回應文字。

<a id="saved-sessions-and-cancellation"></a>

### 已儲存的工作階段與取消操作

登入會先檢查本機儲存，再連線至 LINE。取代可用的已儲存工作階段前會先詢問；回答 **no**
即可保留。`--force` 只略過這個問題，不會略過儲存檢查、無頭環境儲存同意、
同時使用工作階段的檢查或通訊協定錯誤。

Ctrl-C 可取消提示與 QR 輪詢。SIGINT 的結束代碼為 130，SIGTERM 為 143。
結束前會恢復終端機的密碼回顯設定。舊版電子郵件驗證請求仍沿用原有的 HTTP 逾時設定。

如果在新工作階段儲存前取消，本機舊工作階段會保留。不過，若最後的登入請求已到達
LINE 伺服器，原本的 Chrome 類型工作階段可能已被取代。CLI 不會自動重試該請求。
若儲存結果不確定，請檢查 `line auth status --check`，不要還原舊備份或直接重複登入。
若訊號在儲存成功後才到達，仍會回報儲存成功。

透過 SSH 操作時，請使用互動式終端機：

```sh
ssh -t user@host
line login
# 或改用電子郵件登入：
line login --email you@example.com
```

SSH 同樣需要憑證儲存機制。支援的 Linux 主機若沒有已解鎖的金鑰圈，可使用
[無頭環境儲存](#linux-servers-and-headless-storage)。

<a id="daily-commands"></a>

## 日常指令

<a id="let-the-cli-guide-you"></a>

### 跟著提示操作

在互動式終端機中，可以省略部分資訊：

```sh
line messages
line send
line download
line react
line unsend
```

輸入編號選擇聊天室或訊息，也可以搜尋名稱。`n` 和 `p` 可切換頁面，`q` 可取消，
Ctrl-C 可中止。`--json` 與 `--stdin` 會停用提示，因此指令稿必須提供所有必要引數。

<a id="find-chats-and-people"></a>

### 尋找聊天室與聯絡人

```sh
line chats
line chats --search "Family"
line chats --all --limit 50
line chats --show-ids
line contacts
line contacts --search "Alice"
```

`chats` 列出聊天室，`contacts` 列出好友。一般文字輸出預設都顯示 20 筆；
JSON 輸出則顯示所有符合項目，除非指定 `--limit`。一般輸出可用 `--limit 0` 顯示全部。
`chats --all` 也會列出非活躍聊天室。

接受 `CHAT` 的指令可使用完整的聯絡人、多人聊天室或群組 ID，也可使用唯一且完全符合的名稱，
比對時不區分大小寫。含空白的名稱請加引號。找不到名稱或有多個同名聊天室時，
請執行 `line chats --search NAME --show-ids`，再選擇完整 ID。
名稱可能改變，指令稿使用 ID 比較可靠。

查詢不在好友名單中的使用者，請使用完整的使用者 MID（LINE 的使用者識別碼）。
例如，`line messages CHAT_ID --json` 中群組訊息的 `from` 欄位就是傳送者的 MID。

```sh
line contacts --mid USER_MID
line contacts --mid FIRST_USER_MID --mid SECOND_USER_MID --json
```

每個人各指定一次 `--mid`。指令只查詢這些 ID，保留輸入順序、移除完全相同的重複項目，
並列出每個要求查詢的 ID，不會加對方為好友。請使用完整的使用者 MID，
不要使用群組／多人聊天室 ID、顯示名稱或公開的 LINE ID。
CLI 接受舊格式的 `u` 加 32 個小寫十六進位字元，以及現行格式的 `u`／`U` 加 43 個 base64url 字元。

一般輸出會列出每個 MID 與名稱。你設定的自訂聯絡人名稱優先於個人檔案名稱。
`(unavailable)` 表示 LINE 未回傳該個人檔案；`(name unavailable)` 表示有回傳檔案，
但沒有可用的名稱。兩者都不能用來判斷帳號是否已刪除。
`--mid` 不能與 `--search` 或 `--limit` 同時使用。指令稿處理方式請見
[JSON 查詢結果](#json-lookup-results)。

<a id="read-messages"></a>

### 讀取訊息

```sh
line messages "Alice"
line messages "Alice" --limit 50
line messages "Alice" --show-ids
line messages CHAT_ID --json
```

`messages` 取得最近 1–100 則訊息，並依時間由舊到新顯示。
`--show-ids` 會顯示回覆、表情回應、下載與收回訊息所需的 ID。

讀取歷史訊息不會標示已讀，也不會在本機儲存訊息文字。
若 LINE 不再提供原裝置金鑰，部分較舊的加密訊息可能無法讀取。

<a id="send-messages-and-files"></a>

### 傳送訊息與檔案

```sh
line send "Alice" --text "Hello!"
line send "Alice" --stdin < message.txt
line send "Alice" --text "Sounds good" --reply-to MESSAGE_ID
line send "Family group" --file ./report.pdf
line send "Family group" --file ./report.pdf --reply-to MESSAGE_ID
```

`--text`、`--stdin`、`--file` 必須擇一使用。文字不能為空，長度上限為 10,000 個 UTF-16 單位。
一般檔案最大為 20 MiB。透過 `--file` 傳送的圖片、影片與音訊都會以一般檔案呈現。

聊天室支援時，CLI 會使用 Letter Sealing。缺少金鑰、群組成員資料不完整，或傳輸失敗時，
加密傳送會中止，不會默默改傳明文。只有明確執行群組傳送，且掌握所有目前成員時，
才能註冊新的群組金鑰。

加上 `--json` 可查看訊息 ID、是否加密、是否註冊群組金鑰，以及請求序號。

> [!CAUTION]
> CLI 對每次傳送及其他遠端變更都只嘗試一次。若未收到回應，請先到 LINE 確認再重試：
> 操作可能已經成功。

<a id="download-or-change-a-message"></a>

### 下載附件或操作訊息

使用 `line messages CHAT --show-ids` 或 `--json` 找出訊息 ID：

```sh
line download "Alice" --message MESSAGE_ID --output ./received.pdf
line download "Alice" --message IMAGE_MESSAGE_ID --output ./photo.jpg
line react "Alice" --message MESSAGE_ID --reaction love
line react "Alice" --message MESSAGE_ID --remove
line unsend "Alice" --message MY_MESSAGE_ID
```

表情回應可選 `like`、`love`、`laugh`、`surprise`、`sad` 或 `angry`。
只能收回自己傳送的訊息，且須符合 LINE 伺服器的規則。

這些指令會在指定聊天室最近 100 則訊息中搜尋。下載不會覆寫既有檔案；
儲存前會驗證加密資料，遠端中繼資料也不能決定輸出路徑。

`download` 支援最大 20 MiB 的圖片、影片、音訊與一般檔案訊息，傳輸逾時為兩分鐘。
儲存的是完整且未轉換的媒體內容，因此變更副檔名不會轉換格式。
不支援含外部 `DOWNLOAD_URL` 的訊息。讀取或下載都不會註冊群組加密金鑰。

若要把附件內容傳給其他指令，請明確指定聊天室與訊息 ID：

```sh
# Bash 或其他支援 pipefail 的 shell：
set -o pipefail
line download CHAT_ID --message MESSAGE_ID --output - | consumer
```

將 `consumer` 換成你的程式。此模式的 stdout 只有附件內容，診斷訊息寫入 stderr。
不提供互動式提示，也不能使用 `--json`，且 CLI 拒絕將二進位資料直接寫到終端機。
若真的要建立名稱為 `-` 的檔案，請使用 `--output ./-`。

加密媒體會在輸出第一個位元組前完成完整性驗證。下載或驗證失敗時，不會輸出任何附件內容。
但開始輸出後，管線中斷或取消操作可能留下不完整的內容，因此請檢查結束代碼並使用 `pipefail`。
存成檔案時，建議使用 `--output PATH`；shell 的 `>` 重新導向可能在 CLI 執行前就清空既有檔案。

<a id="watch-new-events"></a>

### 接收新事件

```sh
line watch
line watch --timeout 30s
line watch --limit 10
line watch --from-now
```

`watch` 每行向 stdout 寫入一個 JSON 事件，狀態訊息則寫入 stderr。
首次執行從 LINE 目前的 revision 開始；之後從已儲存的檢查點繼續。
`--from-now` 會捨棄檢查點，只接收新事件。

| 事件 | 意義 |
| --- | --- |
| `message` | 傳送或收到一則訊息。 |
| `operation` | 其他 LINE 通知。 |
| `resync_required` | LINE 回報事件有缺漏；請重新取得聊天室與訊息。 |

接收端可利用 `revision` 去除重複事件。如果程序在輸出事件後、儲存檢查點前停止，
最後一個事件可能再次出現。同一時間只能執行一個 watcher。

<a id="updating-line-cli"></a>

## 更新 LINE CLI

```sh
line update --check
line update
line update --check --json
```

`update` 會顯示已安裝版本、GitHub 最新穩定版、安裝方式，以及找到的執行檔。
不需要登入 LINE。`--check` 只檢查並顯示下一步，不會安裝或取得工作階段鎖定。
傳訊指令不會檢查更新。

| 安裝方式 | `line update` 的行為 |
| --- | --- |
| macOS／Linux 官方獨立安裝程式 | 有較新穩定版時，下載、驗證並取代執行檔。 |
| Homebrew | 顯示 `brew upgrade line-cli`，執行檔仍由 Homebrew 管理。 |
| Windows 官方獨立安裝程式 | 顯示 PowerShell 安裝指令，請在目前指令結束後執行。 |
| 從原始碼建置 | 提供發行標籤與原始碼建置說明。 |
| 無法辨識的安裝方式 | 顯示發行頁面，請使用原本的安裝方式更新。 |

自動更新需要官方發行版執行檔，以及由官方安裝程式寫在執行檔旁的 `.line-cli-install` 安裝紀錄。
手動複製的執行檔、缺少此紀錄的舊安裝與原始碼建置都不會自動被取代。
請用原本的方式更新；重新執行官方安裝程式也會建立安裝紀錄。
支援自訂獨立安裝目錄。Windows 更新指令會把 `LINE_CLI_INSTALL_DIR` 設為偵測到的目錄，保留自訂位置。

更新前請停止所有指令與 watcher，包括會自動重啟 watcher 的服務。
自動取代會取得 watcher 與工作階段鎖定，但不讀取憑證。任一鎖定無法取得時就不會繼續。
不要同時執行外部安裝程式。

更新程式從已檢查的發行標籤下載資產，依該版本的 SHA-256 檢查碼驗證封存檔，
在同一目錄準備新執行檔，再以不可分割的重新命名操作取代。
下載、檢查碼或解壓縮失敗時，現有執行檔會保留。下載與解壓縮都有大小限制。
不會使用 sudo、重試安裝、降級較新版本，或自行推測開發版／預先發行版的先後順序。

`--json` 向 stdout 寫入一筆結果，診斷訊息寫入 stderr，安裝行為與一般輸出相同。
若只要唯讀檢查，請**同時使用 `--check` 與 `--json`**。
欄位包括 `current_version`、`latest_version`、`status`、`installation`、`executable`、
`can_self_update`、`release_url`，以及選填的 `instructions` 與 `upgrade_command`。

檢查狀態為 `update_available`、`up_to_date`、`ahead` 或 `unknown_version`。
安裝成功為 `updated`，失敗為 `failed`。`updated_unconfirmed` 表示已取代執行檔，
但無法同步目錄：重試前請檢查 `line version`。
`current_version` 一律記錄啟動指令時的版本；`updated` 後，`latest_version` 就是已安裝版本。
`can_self_update` 表示是否支援自動更新，不代表一定有新版。

檢查成功與顯示手動更新說明時都以 0 結束，即使有可用更新也是如此。
檢查或安裝失敗時則回傳非零代碼。初次檢查失敗不輸出 JSON；安裝失敗會先輸出結果再以錯誤結束。
取消操作使用一般訊號結束代碼。

<a id="use-from-scripts"></a>

## 用於指令稿

<a id="json-output-and-exit-codes"></a>

### JSON 輸出與結束代碼

支援的指令可加上 `--json`：

```sh
line whoami --json
line contacts --json
line contacts --mid USER_MID --json
line chats --search "Family" --json
line messages CHAT_ID --json
line send CHAT_ID --stdin --json < message.txt
```

JSON 寫入 stdout，診斷訊息寫入 stderr。成功與說明指令的結束代碼為 0；
一般 CLI 或網路錯誤為 1。儲存錯誤有[獨立的結束代碼](#storage-exit-codes)。
SIGINT 為 130，SIGTERM 為 143。空的結果清單以 `[]` 表示。

| 指令 | 常用 JSON 資訊 |
| --- | --- |
| `whoami` | 個人資料與帳號 ID。 |
| `contacts` | 好友名稱與 ID。 |
| `contacts --mid MID` | 每個不重複的查詢 MID 各一筆，附查詢狀態。 |
| `chats` | ID、類型、未讀數量，以及選填的活動時間。 |
| `messages` | ID、傳送者、時間、內容、加密資訊與狀態。 |
| `send` | 訊息 ID、聊天室 ID、加密資訊、群組金鑰註冊與序號。 |
| `download` | 輸出路徑、位元組數與訊息 ID。 |
| `react`、`unsend` | 操作、聊天室 ID、訊息 ID 與序號。 |
| `watch` | 每行一個 JSON 事件。 |
| `update` | 已安裝／最新版本、狀態、安裝方式與下一步。只檢查請用 `--check --json`。 |

即使部分訊息無法解密，`messages --json` 仍會輸出完整陣列，附上每則訊息的狀態，
然後以 1 結束。不會印出加密資料區塊或原始伺服器回應內容。

<a id="json-lookup-results"></a>

### JSON 查詢結果

`contacts --mid MID --json` 即使只查一個 ID，也會回傳陣列。
每筆資料包含原有的 `mid`、`displayName`、`displayNameOverridden`、`statusMessage`、
`picturePath`，以及以下欄位：

| 欄位 | 意義 |
| --- | --- |
| `effectiveDisplayName` | 自訂名稱；未設定時使用個人檔案名稱。沒有可用名稱時為空。 |
| `status` | LINE 回傳個人檔案時為 `resolved`；未回傳該 MID 時為 `unavailable`。 |

查不到個人檔案仍會保留一筆資料，包含要求的 `mid`，其餘名稱與個人資料欄位為空。
有回傳個人檔案也可能沒有名稱，顯示時可改用 MID。請用 `mid` 對應資料，因為重複輸入的 ID 只出現一次。

全部個人檔案都回傳時，以 0 結束。任一檔案無法取得時，會輸出完整 JSON 陣列，
在 stderr 回報數量，並以 1 結束。指令稿仍可使用這份部分結果。
若發生網路、驗證或回應格式錯誤，即使前一批查詢成功，也不會輸出結果陣列。

<a id="account-and-session"></a>

## 帳號與工作階段

<a id="where-your-session-is-stored"></a>

### 工作階段的儲存位置

CLI 為每位作業系統使用者儲存一個 LINE 帳號。

| 系統 | 儲存方式 |
| --- | --- |
| macOS | 鑰匙圈（Keychain）。 |
| Linux | 加密的工作階段檔案；金鑰存放在 Secret Service。 |
| Windows | 使用目前使用者的 DPAPI 加密工作階段檔案。 |

密碼只用於登入，不會儲存。更新工作階段時，指令會取得鎖定。
若另一個指令回報工作階段忙碌，請等目前更新完成後再試。

不連線至 LINE，檢查本機儲存：

```sh
line auth status
line auth status --check --json
```

`auth status` 檢查本機儲存是否可存取，不會連線至 LINE 或更新權杖，
也不能證明已儲存的登入仍然有效。無法存取時會回報無法讀取，不會當作已登出。
`--check` 會在不觸發解鎖提示的前提下測試是否可寫入。
macOS Keychain 與 Linux Secret Service 可能回報 `interactive_check_required`；
完整寫入檢查會在互動式登入時執行。無頭環境重新開機後的存取狀態，
在你實際測試主機前仍為 `expected_not_verified`。

移除本機工作階段：

```sh
line logout
```

登出不會撤銷 LINE 伺服器上的工作階段。

<a id="token-refresh"></a>

### 權杖更新

一般存取權杖約七天後會到達更新門檻，但這不代表已儲存工作階段的固定有效期限。
只要已儲存的更新權杖仍有效，CLI 會自動更新存取權杖，重新啟動後也一樣。
若更新持續失敗或缺少更新權杖，可能需要執行 `line login`。

唯讀請求可更新憑證後重試一次。傳送、表情回應、收回與上傳都不會自動重送；
結果不確定時，請先到 LINE 確認再重複操作。watcher 會視需要使用已儲存憑證重新連線。

只有伺服器明確發出登出訊號，才會將已儲存工作階段標為失效。
權杖過期、一般 HTTP 401／403、更新遭拒或網路失敗，本身都不會使它被標為失效。
伺服器登出訊號也不一定能判斷是否被另一個用戶端取代。
若權杖輪替後發生儲存失敗，CLI 會停止後續請求，因為無法確保新權杖已儲存。

更新時機、重試規則與呼叫路徑證據，請見[權杖與工作階段稽核](../TOKEN_SESSION.md)。

<a id="linux-servers-and-headless-storage"></a>

## Linux 伺服器與無頭環境儲存

<a id="enroll-without-an-unlocked-keyring"></a>

### 沒有已解鎖金鑰圈時的首次設定

支援的 Linux 主機若沒有已解鎖的 Secret Service 金鑰圈，可透過互動式操作設定無頭環境儲存：

```sh
line login --headless --email you@example.com
line auth status
line auth status --check --json
```

範例使用電子郵件登入。`line login --headless` 則使用實驗性 QR 登入，儲存保護相同。
首次設定仍需要互動式終端機；`--headless` 不會讓登入變成無人值守。

連線至 LINE 前，CLI 會要求你接受 **Host key; no TPM**（主機金鑰、無 TPM）保護。
`--force` 不會略過此步驟。無頭環境儲存可防止其他無特權使用者存取，
但無法防範 root、以你的帳號執行的惡意程式，或持有完整磁碟副本的人。此方式不提供 TPM 保護。
若在新工作階段儲存前取消，不會留下新的本機工作階段。

設定完成後，直接使用一般指令，不需要儲存選項。再次登入會沿用所選後端。
`login --headless` 不會轉換或覆寫既有的原生工作階段。

無頭環境儲存需要可信任的 `/usr/bin/systemd-creds` 輔助程式及使用者憑證代理服務。
互動式指令、SSH、cron 與服務都必須使用同一個 Unix 帳號與設定目錄。
接受 systemd 256–259；Debian 13／systemd 257 與 Ubuntu 26.04／systemd 259
已在可拋棄虛擬機中驗證。其他接受的版本仍須通過本機檢查。
較舊或尚未審查的新版會遭拒。UniPi 與實體 TPM 的行為尚未驗證。

<a id="move-an-existing-linux-session"></a>

### 移轉既有 Linux 工作階段

將可存取的原生 Linux 工作階段移至無頭環境儲存：

```sh
line auth migrate --storage=headless
```

移轉需要終端機，且必須明確接受相同的純主機金鑰保護。
請先停止 watcher、自動化與舊版 CLI。移轉會保留權杖、加密金鑰、請求序號與事件檢查點，
不會連線至 LINE，也不會要求密碼。

若移轉中途停止，請再次執行相同指令。不要刪除 `migration.pending` 或手動取代 `session.enc`。
清理完成前，`line auth status` 會回報 `migration_pending`。
復原流程請見[無頭環境儲存內部機制](../internal/session/HEADLESS.md)。

<a id="run-a-watcher-without-a-login-session"></a>

### 未登入作業系統時執行 watcher

請用專用的無特權帳號互動式完成首次設定。重新開機後，以服務將使用的同一個使用者與環境
執行 `line auth status --check`。保持 `HOME` 與 `XDG_CONFIG_HOME` 一致。
服務或 cron 工作由你自行安裝與管理，CLI 不會建立。

systemd 使用者服務可將以下範例存為該帳號的 `~/.config/systemd/user/line-watch.service`。
若執行檔位於別處，請修改 `ExecStart`。`%h` 代表帳號的家目錄。
使用者服務不要加上 `User=`。

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

這份設定在 [issue #3](https://github.com/kongesque/line-cli/issues/3) 回報的
Debian 13／systemd 257.13 ARM64 環境中可用。啟用前請先在自己的主機上確認。
`AF_UNIX` 讓憑證代理服務能運作；`AF_INET` 與 `AF_INET6` 允許 LINE 連線與 DNS。

```sh
systemctl --user daemon-reload
systemctl --user start line-watch.service
systemctl --user status line-watch.service
# 確認儲存與 watcher 正常後再啟用：
systemctl --user enable line-watch.service
```

若要在沒有互動式登入時啟動使用者服務，可能需要管理員啟用 lingering。
若改用系統服務，請加上 `User=linebot`，兩個環境變數都使用明確的 `/home/linebot` 路徑，
並設定 `WantedBy=multi-user.target`。

watch 輸出包含私人訊息，請限制 journal 或輸出檔案的存取權限。
cron 同樣應使用相同帳號與路徑，並設定 `umask 077`。

<a id="diagnose-a-headless-service"></a>

### 無頭環境服務疑難排解

以下 systemd 設定曾在 [issue #3](https://github.com/kongesque/line-cli/issues/3)
的使用者服務環境中失敗：

| 設定 | 觀察結果 |
| --- | --- |
| `PrivateTmp=true` | 無頭環境輔助程式無法使用；結束代碼 69。 |
| `ProtectSystem=strict` | 無頭環境輔助程式無法使用；結束代碼 69。 |
| `ProtectHome=read-only` | 非零結束代碼，包含檔案寫入失敗。 |

基本服務設定先不要加入上述選項或 `PrivateUsers=`。
只加上 `ReadWritePaths=%h/.config/line-cli` 並未解決回報的問題。
這些是單一使用者服務環境的觀察，並非適用於所有系統服務的通則。

systemd 建立的使用者命名空間可能讓個別使用者服務看不到主機的 root UID。
使用 `PrivateUsers=true` 時，root 擁有的輔助程式可能顯示為沒有對應的擁有者
（[systemd 257 文件](https://github.com/systemd/systemd/blob/v257/man/systemd.exec.xml)）。
CLI 會拒絕使用無法驗證擁有者與權限的輔助程式。
唯讀掛載也可能阻擋鎖定、路徑紀錄、更新權杖、請求序號與事件檢查點的寫入。
`$XDG_CONFIG_HOME/line-cli` 與 `$HOME/.config/line-cli` 都必須可寫，即使它們是不同目錄。

以服務使用者執行 `line auth status --check --json`，比較互動式環境與暫時服務單元中的結果，
兩者須使用相同環境與強化設定。例如，必要時請調整執行檔路徑：

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

此檢查只在本機執行，不會連線至 LINE。每次只改一項沙箱設定：

- `headless_helper_untrusted`（結束代碼 69）：輔助程式或上層目錄未通過擁有者／權限檢查。
  檢查文字錯誤訊息提到的沙箱設定，也要確認實際安裝權限。
- `headless_unavailable`：探索輔助程式、版本支援或以 root 執行的檢查失敗。文字錯誤訊息會指出階段。
- `storage_unavailable`：可能是憑證代理服務或解封失敗。不會顯示輔助程式的 stderr。

考慮重新設定或刪除資料前，請先恢復可用的服務環境。
若服務已停止，先修復儲存，再執行 `systemctl --user reset-failed line-watch.service` 並重新啟動。
結束代碼 69 會阻止 watcher 自動重啟。此重啟規則不適用於傳送或其他遠端變更。

<a id="linux-session-files-and-upgrades"></a>

### Linux 工作階段檔案與升級

Linux 的加密工作階段檔案與兩個程序鎖定都位於 `$XDG_CONFIG_HOME/line-cli`
（通常是 `~/.config/line-cli`）。改變 `XDG_CACHE_HOME` 不會改變鎖定位置。
應用程式目錄權限應為 `0700`，檔案為 `0600`。
CLI 會拒絕不安全的擁有者、檔案類型、應用程式目錄或檔案的符號連結，以及硬連結檔案。
不會自動修改既有上層目錄的權限。

登入會在詢問本機輸入前檢查儲存，再於連線至 LINE 前重新確認工作階段與儲存識別資訊。
檢查使用獨立的暫存憑證項目或加密檔案，保留目前工作階段。
若儲存無法讀取或已損毀，請先恢復存取，或明確登出以移除本機工作階段。
若輸入資料期間另一個登入或登出指令改變了工作階段，請重新執行指令。

Linux 也會在 `$HOME/.config/line-cli` 的 `native-paths.json` 記錄曾偵測到的工作階段路徑。
若舊版曾使用其他 `XDG_CONFIG_HOME`，移轉或登出前，請對每個舊位置各執行一次 `line auth status`。
這能避免 CLI 刪除其他已知目錄仍需要的金鑰。不要刪除路徑紀錄或鎖定檔案來省略步驟。

升級至此鎖定配置前，請先停止舊指令與 watcher；不支援新舊版同時執行。
登出後仍應保留鎖定檔案。多個設定目錄不能用來支援多帳號，因為原生 Linux 儲存
在每個 Secret Service 金鑰圈中只使用一個金鑰識別。

若錯誤指出資料是否已確實寫入磁碟仍不確定，檔案可能已變更。
請重複相同的本機操作，不要還原舊副本。登出會保留私有復原紀錄，可再次執行以完成中斷的清理。

<a id="storage-exit-codes"></a>

### 儲存錯誤的結束代碼

| 代碼 | 意義 |
| --- | --- |
| 65 | 格式無效、缺少金鑰、驗證失敗或保護方式不符。 |
| 69 | 儲存或輔助程式無法使用或逾時。 |
| 74 | 無法確定資料已確實寫入磁碟，或測試資料清理失敗。 |
| 75 | 本機資源競爭、登入期間儲存變更，或輔助程式取消。 |
| 78 | 需要設定、同意、移轉、修復或互動式檢查。 |

訊號優先：SIGINT 為 130，SIGTERM 為 143。其他 CLI 或網路錯誤為 1。
儲存無法使用時，`auth status --json` 仍可能先輸出狀態物件，再以對應的非零代碼結束。

<a id="reference"></a>

## 參考資訊

<a id="command-help"></a>

### 指令說明

內建說明提供目前的選項清單：

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

### 從原始碼建置

需要 Git 與 Go 1.26 以上版本。macOS 還需要 Xcode Command Line Tools 與 CGO，以支援 Keychain。

macOS 或 Linux：

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

貢獻者檢查：

```sh
go test -race ./internal/... ./cmd/line ./pkg/line/... ./pkg/e2ee
go vet ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
go build -trimpath -o bin/line ./cmd/line
```

一般測試使用模擬 API 與憑證。實際連線測試預設停用，且需要明確授權。
取消操作測試使用合成的子程序。macOS／Linux 的密碼終端機測試會在可用時使用
Python 3 標準函式庫的 PTY 功能。原生 Linux Secret Service 整合測試需要明確啟用的
可拋棄 D-Bus 工作階段；Windows DPAPI 測試使用暫存檔案。

<a id="current-limitations"></a>

### 目前限制

- 每位作業系統使用者只能儲存一個 LINE 帳號。
- QR 登入為實驗性功能，不支援停用 Letter Sealing 的帳號。
- 只能讀取近期歷史訊息，每次最多 100 則。
- 支援傳送一般檔案，但不支援貼圖或專用媒體傳送。
- 支援下載最大 20 MiB 的圖片、影片、音訊與檔案，不支援外部媒體網址。
- 讀取訊息不會標示已讀。
- 發行版執行檔尚未經過數位簽署或公證。
- LINE 通訊協定變更可能影響相容性。

LINE CLI 是衍生自 `beeper/line` 的獨立專案，並非 LINE 官方產品，
也不需要 Beeper 或 Matrix 家伺服器。
