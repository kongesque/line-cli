# LINE CLI：個人 LINE 帳號的指令列用戶端

[English](README.md) | 繁體中文（台灣） | [日本語](README.ja.md) | [ภาษาไทย](README.th.md)

**LINE CLI** 是以 Go 撰寫的非官方工具，讓你能在終端機使用個人 LINE 帳號，
讀取聊天室、傳送訊息與檔案、下載附件，並接收即時事件。
支援 **macOS、Linux 與 Windows**，可使用互動式提示或 JSON 輸出，
不需要機器人帳號或設定 LINE Messaging API。

[![CI](https://github.com/kongesque/line-cli/actions/workflows/cli.yml/badge.svg)](https://github.com/kongesque/line-cli/actions/workflows/cli.yml)
[![Release](https://img.shields.io/github/v/release/kongesque/line-cli?filter=v*&label=release)](https://github.com/kongesque/line-cli/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/kongesque/line-cli)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[安裝](#install-line-cli) · [快速上手](#quick-start-send-your-first-line-message) · [指令](#line-messaging-commands) · [自動化](#automate-line-with-json-and-shell-scripts) · [完整指南](docs/CLI.zh-TW.md)

![LINE CLI：在終端機傳送個人 LINE 訊息、分享檔案與自動化](banner.png)

本 README 說明開發分支。已發布版本請查看該標籤的 README 與 `line COMMAND --help`。
升級前請參閱[變更紀錄](CHANGELOG.md)。

<a id="install-line-cli"></a>

## 安裝 LINE CLI

### macOS：Homebrew

```sh
brew install kongesque/tap/line-cli
```

### macOS 與 Linux：獨立執行檔安裝程式

```sh
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.sh | sh
```

安裝程式會偵測作業系統與 CPU 架構、驗證發行檔的檢查碼，並將 `line`
安裝至 `~/.local/bin`。若提示需要套用 `PATH` 設定，請重新開啟終端機。
若要在目前的 shell 直接使用，請執行 `export PATH="$HOME/.local/bin:$PATH"`。

**Linux 桌面：** Debian 或 Ubuntu 請在登入前安裝憑證儲存套件：

```sh
sudo apt install libsecret-tools gnome-keyring
```

請從桌面工作階段執行 `line`，並確認 Secret Service 金鑰圈已啟動、解鎖，
且與 `line` 位於同一個 D-Bus 工作階段。沒有桌面金鑰圈的伺服器或 SSH 環境，
請使用下方的無頭環境登入方式。

### Windows：PowerShell

```powershell
irm https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.ps1 | iex
```

安裝程式會驗證檢查碼，並將 `line.exe` 加入使用者的 `PATH`。
必要時請重新開啟 PowerShell。Windows 不需要額外安裝金鑰圈套件。

你也可以[下載發行檔](https://github.com/kongesque/line-cli/releases/latest)
或[從原始碼建置](#build-and-contribute)。發行檔尚未經過數位簽署或公證。

確認安裝結果：

```sh
line version
```

<a id="quick-start-send-your-first-line-message"></a>

## 快速上手：傳送第一則 LINE 訊息

> [!IMPORTANT]
> 登入可能會取代現有的 LINE Chrome 擴充功能或其他 Chrome 類型用戶端工作階段。
> LINE CLI 會為每位作業系統使用者儲存一個帳號。

### 1. 使用手機上的 LINE 登入

```sh
line login
```

使用手機 LINE 的掃描器掃描終端機上的 QR 碼、核准登入，並依提示輸入顯示的 PIN。
請等到出現 **Session saved securely**。QR 碼登入目前為實驗性功能，
且必須啟用 Letter Sealing。

若 LINE 帳號已設定電子郵件與密碼，可改用下列方式登入，QR 碼登入失敗時也適用：

```sh
line login --email you@example.com
```

請依提示輸入密碼（輸入內容不會顯示），並在需要時完成手機驗證。密碼不會被儲存。

<details>
<summary>Linux 伺服器或 SSH：不使用桌面金鑰圈登入</summary>

請使用一般 Unix 使用者帳號與互動式終端機；遠端連線可執行 `ssh -t user@host`。
無頭環境儲存需要 systemd 256–259 的 `/usr/bin/systemd-creds`，
以及可運作的使用者憑證代理服務。

```sh
line login --headless
```

若要使用電子郵件與密碼，請改用 `line login --headless --email you@example.com`。
依提示同意 **Host key; no TPM**：此儲存方式無法防止整顆磁碟遭複製，也不提供 TPM 保護。
首次設定仍需終端機互動操作與手機核准。

等到出現 **Session saved securely** 後，執行 `line auth status --check`。
接著使用相同的 Unix 帳號執行下方指令，不必再加上 `--headless`。

</details>

### 2. 瀏覽聊天室並傳送訊息

```sh
line whoami     # 確認目前帳號
line chats      # 列出聊天室
line messages   # 選擇聊天室並讀取近期訊息
line send       # 選擇收件人並輸入訊息
```

在互動式終端機中，CLI 會提示你補齊所需資料。按 Ctrl-C 可取消，
也能像以下範例一樣直接指定聊天室名稱。

傳送成功後會顯示 `Sent to … · end-to-end encrypted`，或
`Sent to … · transport encrypted (no E2EE)`。

<a id="line-messaging-commands"></a>

## LINE 訊息操作指令

| 想做什麼 | 指令 |
| --- | --- |
| 尋找好友 | `line contacts --search "Alice"` |
| 讀取近期訊息 | `line messages "Alice" --limit 20` |
| 傳送文字訊息 | `line send "Alice" --text "Hello!"` |
| 傳送檔案 | `line send "Alice" --file ./report.pdf` |
| 下載照片、影片、音訊或檔案 | `line download` |
| 串流接收即時事件 | `line watch` |

請將 `Alice` 換成沒有重複的完整聊天室名稱（不區分大小寫），或使用完整的聊天室 ID。
若名稱重複，執行 `line chats --search "Alice" --show-ids`，再使用顯示的完整 ID。
使用 `line COMMAND --help` 查看選項；回覆、表情回應與分頁等進階操作請參閱
[CLI 指南](docs/CLI.zh-TW.md#daily-commands)。

<a id="automate-line-with-json-and-shell-scripts"></a>

## 使用 JSON 與 shell 指令稿自動化 LINE

加入 `--json` 可將結構化資料輸出至 stdout；診斷訊息則輸出至 stderr。
`--json` 與 `--stdin` 會停用互動式提示，請提供所有必要參數。
名稱可能變動，因此指令稿應使用完整的聊天室 ID。

```sh
line chats --search "Family" --json
line messages CHAT_ID --limit 20 --json
line send CHAT_ID --stdin --json < message.txt
line watch --json > events.ndjson
```

從 `line chats --json` 取得 `CHAT_ID`。事件監看輸出為 NDJSON，接收端應依 revision 去除重複事件。
欄位與錯誤格式請參閱 [JSON 參考](docs/JSON.md)。

會變更遠端資料的操作只會嘗試一次。若傳送或其他操作後未收到回應，
請先在 LINE 中確認結果，再決定是否重做。

## Letter Sealing 加密與工作階段安全性

傳送會優先使用 Letter Sealing（端對端加密，E2EE）。只有 LINE 明確回報不支援時，
才會直接透過 HTTPS/TLS 傳送，不再逐次詢問。傳輸仍有加密，但 LINE 可讀取內容。
若要禁止非 E2EE 傳送，請加上 `--require-e2ee`。檔案與文字的加密支援會分開檢查；
金鑰缺少或傳輸失敗時不會停用端對端加密。

詳見[傳送與加密](docs/CLI.zh-TW.md#send-messages-and-files)及
[工作階段儲存](docs/CLI.zh-TW.md#where-your-session-is-stored)。

## 使用限制

- LINE 回報不支援 E2EE 時，會透過 HTTPS/TLS 傳送，但 LINE 可讀取內容。
  使用 `--require-e2ee` 可阻止這類傳送。詳見[LINE 2025 年加密報告（英文）](https://www.lycorp.co.jp/en/privacy-security/security/transparency/encryption-report/2025/)。
- 每頁最多 100 則訊息。使用 `--page --json` 與游標可讀取仍可取得的較舊歷史，
  但不保證完整歷史。讀取不會標為已讀；部分較舊的加密訊息可能無法讀取。
- 檔案傳送與媒體下載上限為 20 MiB。`--file` 保留原始位元組，並顯示為一般檔案。
  目前停用加密的原生圖片傳送；非 E2EE 圖片需要 LINE 明確回報支援。
  不支援原生影音傳送、貼圖傳送及外部媒體 URL 下載。
- LINE CLI 使用 LINE 的 Chrome 類型協定。伺服器變更可能影響相容性，
  部分協定流程仍需要更廣泛的實際環境驗證。

## 更新 LINE CLI

升級前，請先停止正在執行的指令與事件監看程序；不支援新舊版本同時執行。

```sh
line update --check   # 檢查最新版本，不進行安裝
line update           # 支援時更新，否則顯示升級指引
```

Homebrew 請使用 `brew upgrade line-cli`。其他安裝方式請參閱
[更新指南](docs/CLI.zh-TW.md#updating-line-cli)。

## 文件與協助

請閱讀 [CLI 指南](docs/CLI.zh-TW.md)，或瀏覽[文件索引](docs/README.md)（參考文件為英文）。
透過 [GitHub Issues](https://github.com/kongesque/line-cli/issues) 回報錯誤或提出功能需求。
回報時請附上 `line version`、作業系統、指令及已遮蔽敏感內容的錯誤訊息。
請勿提供密碼、權杖、QR 碼登入值或私人訊息。

<a id="build-and-contribute"></a>

## 建置與參與開發

原始碼建置需要 **Go 1.27.2**；macOS 另需 Xcode Command Line Tools 與 CGO。
設定與開發檢查請參閱[原始碼建置](docs/CLI.zh-TW.md#build-from-source)及
[參與開發指南](CONTRIBUTING.md)（英文）。

## 授權與來源

LINE CLI 與 LINE 無關，也未獲 LINE 背書。本專案衍生自
[beeper/line](https://github.com/beeper/line)；共用的協定與加密程式碼保留
上游著作權聲明與來源資訊。Matrix 連接器與 Beeper 部署環境不包含在本專案中。

以 [MIT 授權條款](LICENSE)發布。
