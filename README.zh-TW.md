# LINE CLI：在終端機使用個人 LINE 帳號

繁體中文（台灣） | [ภาษาไทย](README.th.md) | [日本語](README.ja.md) | [English](README.md)

**LINE CLI** 是一套非官方的個人 LINE 帳號指令列用戶端。你可以直接在
終端機讀取與傳送訊息、分享檔案、回覆、加入表情回應、收回訊息、監看
即時事件，也能透過 JSON 串接指令稿與 AI 代理程式工作流程。

LINE CLI 與使用 LINE Messaging API 的工具不同；它使用的是你的個人
LINE 帳號，不是機器人帳號。

![使用 LINE CLI 在終端機操作個人 LINE 帳號](banner.png)

[![CI](https://github.com/kongesque/line-cli/actions/workflows/cli.yml/badge.svg)](https://github.com/kongesque/line-cli/actions/workflows/cli.yml)
[![Release](https://img.shields.io/github/v/release/kongesque/line-cli?filter=cli-v*&label=release)](https://github.com/kongesque/line-cli/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/kongesque/line-cli)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

LINE CLI 支援 macOS、Linux 與 Windows，並提供 Letter Sealing 端對端加密
及作業系統原生的認證資料儲存機制。

> [!IMPORTANT]
> LINE 一次只允許一個 Chrome 類型的工作階段。使用 `line` 登入時，可能會
> 取代現有的 LINE Chrome 擴充功能或其他 Chrome 類型用戶端工作階段。
> CLI 會為每位作業系統使用者儲存一個帳號。

## 功能

- 直接在終端機使用個人 LINE 帳號
- 尋找聯絡人、群組與聊天室
- 讀取近期訊息記錄
- 傳送單行或多行文字訊息
- 傳送最大 20 MiB 的一般檔案
- 回覆既有訊息
- 新增或移除標準表情回應
- 收回自己傳送的訊息
- 以 NDJSON 串流接收即時 LINE 事件
- 輸出 JSON，供 shell 指令稿與自動化流程使用
- 使用作業系統原生的認證資料儲存機制保護已儲存的工作階段
- 若聊天室支援，則使用 Letter Sealing 加密

## 安裝 LINE CLI

### macOS 與 Linux

使用一行指令安裝最新版本：

```sh
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.sh | sh
```

安裝程式會自動偵測作業系統與 CPU 架構、驗證發行檔的檢查碼、將 `line` 安裝到
`~/.local/bin`，並為常見 shell 設定 `PATH`。如果安裝程式提示，請重新開啟終端機。

在 Linux 執行 `line login` 前，請先安裝認證資料儲存工具。Debian／Ubuntu 可執行：

```sh
sudo apt install libsecret-tools gnome-keyring
```

Secret Service 金鑰圈必須在同一個 D-Bus 工作階段中執行並保持解鎖。

> [!IMPORTANT]
> 透過 SSH 或無頭環境使用 Linux 時，只安裝套件還不夠。如果 CLI 所在的
> D-Bus 工作階段沒有已解鎖的 Secret Service，手機驗證完成後，登入會因
> `could not save Secret Service` 錯誤而失敗。

macOS 也可以使用 Homebrew：

```sh
brew install kongesque/tap/line-cli
```

Homebrew 會從含版本標籤的原始碼建置，並安裝 `line` 指令。這種安裝方式
可避免從瀏覽器下載未簽署檔案時出現的 Gatekeeper 警告。

日後可用以下指令升級：

```sh
brew upgrade line-cli
```

### Windows（PowerShell）

請在 PowerShell 執行：

```powershell
irm https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.ps1 | iex
```

安裝程式會自動偵測 CPU 架構、驗證發行檔的檢查碼、將 `line.exe` 安裝到使用者
設定檔，並加入使用者的 `PATH`。
Windows 使用內建的 DPAPI，因此不需要額外安裝金鑰圈套件。目前發布的
執行檔尚未經過數位簽署。如果無法立即執行 `line`，請重新開啟 PowerShell。

### 從原始碼建置

建置前須先安裝 Git 與 Go 1.26 或更新版本。在 macOS／Linux 上執行：

```sh
git clone https://github.com/kongesque/line-cli.git
cd line-cli
./install.sh
line help
```

Windows 請使用 [PowerShell 建置指令](CLI.md#build-from-source)。
`install.sh` 需要 POSIX shell，無法直接在 PowerShell 中執行。

從原始碼建置時，仍須符合上述 Linux 金鑰圈需求。Windows PowerShell
指令與貢獻者檢查方式請參閱[原始碼建置指南](CLI.md#build-from-source)。

## 在終端機使用 LINE

你的 LINE 帳號必須先設定電子郵件地址與密碼。登入採互動式流程，且需在
手機上核准。

```sh
line login
line whoami
line chats
line messages "Family group" --limit 10
line send "Alice" --text "Hello!"
```

以參數指定名稱時，名稱必須完全相符，且不得與其他名稱重複。若要透過
互動介面選擇聊天室，執行指令時不要提供目標：

```sh
line messages
line send
line react
line unsend
```

其他常用操作：

```sh
line send "Alice" --file ./report.pdf
line send "Alice" --text "Sounds good" --reply-to MESSAGE_ID
line react "Alice" --message MESSAGE_ID --reaction love
line download "Alice" --message MESSAGE_ID --output ./received.pdf
```

使用 `line COMMAND --help` 查看各指令的選項與範例。

## JSON 與自動化

```sh
line chats --search "Family" --json
line messages "Alice" --limit 20 --json
line send "Alice" --stdin < message.txt
line watch --json > events.ndjson
```

JSON 資料會輸出至 stdout，診斷訊息則輸出至 stderr。匯出的訊息與事件
屬於私人對話資料，請妥善保管。

會變更遠端資料的操作只會嘗試一次，且不會自動重試。如果傳送訊息或上傳
檔案後沒有收到回應，請先在 LINE 中確認結果，再決定是否重試，以免產生
重複內容。

## 安全性與隱私權

若帳號與聊天室皆支援，LINE CLI 會使用 Letter Sealing。若缺少金鑰、格式
錯誤或網路傳輸失敗，原應加密的訊息不會在未告知的情況下改用明文傳送。
傳送結果會顯示是否使用加密。

你的密碼不會被儲存。工作階段由作業系統提供保護：

| 平台 | 認證資料儲存方式 |
| --- | --- |
| macOS | Keychain（鑰匙圈） |
| Linux | AES-GCM 加密的工作階段檔案；包裝金鑰存放於 Secret Service |
| Windows | 目前使用者的 DPAPI |

LINE CLI 採用 LINE Chrome 類型的通訊協定。伺服器端的變更可能影響相容性。

## 目前限制

- 每位作業系統使用者只能儲存一個帳號
- 只能讀取近期記錄，每次最多 100 則訊息
- 僅支援一般檔案傳輸，不支援貼圖或特殊媒體訊息
- 讀取訊息不會將訊息標示為已讀
- 部分通訊協定流程仍需更廣泛的實際環境驗證

## 文件

- [完整 CLI 指令參考與疑難排解](CLI.md)
- [Letter Sealing 實作筆記](readme/LETTER_SEALING.md)
- [貢獻指南與套件結構](AGENTS.md)

## 開發

```sh
./build.sh
go test -race ./internal/... ./cmd/line ./pkg/line/... ./pkg/e2ee
go vet ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
```

CI 會在 Linux、macOS 與 Windows 上執行，並包含各平台原生認證資料儲存
機制的檢查。

## 授權與來源

LINE CLI 並非 LINE 官方產品，亦未獲得 LINE 認可。本專案衍生自
[beeper/line](https://github.com/beeper/line)；共用的通訊協定與密碼學程式碼
保留上游的著作權聲明及來源資訊。本專案不包含 Matrix connector 與 Beeper
部署環境。

本專案採用 [MIT 授權條款](LICENSE)。
