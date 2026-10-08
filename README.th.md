# LINE CLI: ไคลเอนต์บรรทัดคำสั่งสำหรับบัญชี LINE ส่วนตัว

[English](README.md) | [繁體中文（台灣）](README.zh-TW.md) | [日本語](README.ja.md) | ภาษาไทย

**LINE CLI** คือไคลเอนต์บรรทัดคำสั่งของ LINE แบบโอเพนซอร์สที่ไม่เป็นทางการ เขียนด้วย Go
ใช้ส่งข้อความ LINE จากเทอร์มินัล อ่านแชทส่วนตัวและแชทกลุ่ม แชร์ไฟล์ ดาวน์โหลดไฟล์แนบ
และรับเหตุการณ์แบบเรียลไทม์ได้ ใช้คำแนะนำแบบโต้ตอบสำหรับการส่งข้อความในชีวิตประจำวัน
หรือใช้ JSON เพื่อเชื่อมต่อกับ shell script และเวิร์กโฟลว์ของ AI agent

รองรับ **macOS, Linux และ Windows** พร้อมการเข้ารหัสจากต้นทางถึงปลายทางด้วย
Letter Sealing เมื่อห้องแชทรองรับ ลงชื่อเข้าใช้ด้วยบัญชี LINE ส่วนตัวของคุณได้เลย
โดยไม่ต้องมีบัญชีบอตหรือตั้งค่า LINE Messaging API

[![CI](https://github.com/kongesque/line-cli/actions/workflows/cli.yml/badge.svg)](https://github.com/kongesque/line-cli/actions/workflows/cli.yml)
[![Release](https://img.shields.io/github/v/release/kongesque/line-cli?filter=v*&label=release)](https://github.com/kongesque/line-cli/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/kongesque/line-cli)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[ติดตั้ง](#install-line-cli) · [เริ่มใช้งาน](#quick-start-send-your-first-line-message) · [คำสั่ง](#line-messaging-commands) · [ระบบอัตโนมัติ](#automate-line-with-json-and-shell-scripts) · [คู่มือฉบับเต็ม](docs/CLI.th.md)

![LINE CLI: ส่งข้อความ LINE ส่วนตัว แชร์ไฟล์ และทำงานอัตโนมัติผ่านเทอร์มินัล](banner.png)

<a id="install-line-cli"></a>

## ติดตั้ง LINE CLI

### macOS: Homebrew

```sh
brew install kongesque/tap/line-cli
```

### macOS และ Linux: ตัวติดตั้งไบนารีแบบ standalone

```sh
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.sh | sh
```

ตัวติดตั้งจะตรวจหาระบบปฏิบัติการและสถาปัตยกรรม CPU ตรวจสอบ checksum ของรีลีส
และติดตั้ง `line` ลงใน `~/.local/bin` หากมีคำแนะนำให้เปิดเทอร์มินัลใหม่
ให้ทำตามเพื่อใช้การตั้งค่า `PATH`

**Linux เดสก์ท็อป:** ก่อนเข้าสู่ระบบ ตรวจสอบว่า Secret Service keyring ทำงานและปลดล็อกอยู่
ใน D-Bus session เดียวกับ `line` สำหรับ Debian หรือ Ubuntu ให้ติดตั้งเครื่องมือต่อไปนี้:

```sh
sudo apt install libsecret-tools gnome-keyring
```

**Linux เซิร์ฟเวอร์หรือ SSH:** การติดตั้งแพ็กเกจอย่างเดียวไม่เพียงพอ บนโฮสต์ที่รองรับ
`line login --headless` ใช้ที่เก็บข้อมูลแบบ host key ได้โดยไม่ต้องมี keyring ที่ปลดล็อก
การตั้งค่าครั้งแรกยังต้องทำแบบโต้ตอบ วิธีจัดเก็บนี้ไม่ป้องกันการคัดลอกดิสก์ทั้งลูก
และไม่มีการป้องกันด้วย TPM ดู[การตั้งค่า headless การย้ายเซสชัน และการใช้งานบริการ](docs/CLI.th.md#linux-servers-and-headless-storage)

### Windows: PowerShell

```powershell
irm https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.ps1 | iex
```

ตัวติดตั้งจะตรวจสอบ checksum และเพิ่ม `line.exe` ลงใน `PATH` ของผู้ใช้
เปิด PowerShell ใหม่หากจำเป็น Windows ไม่ต้องติดตั้งแพ็กเกจ keyring เพิ่มเติม

คุณยังสามารถ[ดาวน์โหลดรีลีส](https://github.com/kongesque/line-cli/releases/latest)
หรือ[บิลด์จากซอร์สโค้ด](#build-and-contribute)ได้ ไบนารีของรีลีสยังไม่มีลายเซ็นดิจิทัล
และยังไม่ได้ผ่าน notarization

ตรวจสอบการติดตั้ง:

```sh
line version
line help
```

<a id="quick-start-send-your-first-line-message"></a>

## เริ่มใช้งาน: ส่งข้อความ LINE แรกของคุณ

> [!IMPORTANT]
> การเข้าสู่ระบบอาจแทนที่เซสชันเดิมของส่วนขยาย LINE สำหรับ Chrome หรือไคลเอนต์อื่น
> ที่ใช้เซสชันแบบ Chrome โดย LINE CLI จะบันทึกได้หนึ่งบัญชีต่อผู้ใช้ระบบปฏิบัติการ

### 1. ลงชื่อเข้าใช้ด้วย LINE บนโทรศัพท์

```sh
line login
```

ใช้ตัวสแกนใน LINE บนโทรศัพท์สแกน QR code ที่แสดงในเทอร์มินัล อนุมัติการเข้าสู่ระบบ
และกรอก PIN ที่แสดงหากระบบขอ รอจนเห็นข้อความ **Session saved securely**
การเข้าสู่ระบบด้วย QR ยังเป็นฟีเจอร์ทดลองและต้องเปิดใช้งาน Letter Sealing

หากบัญชีของคุณตั้งค่าอีเมลและรหัสผ่านไว้แล้ว สามารถใช้:

```sh
line login --email you@example.com
```

รหัสผ่านจะไม่แสดงขณะพิมพ์และจะไม่ถูกบันทึก การเข้าสู่ระบบจะตรวจสอบที่เก็บข้อมูล
และถามก่อนแทนที่เซสชันที่บันทึกไว้และยังใช้งานได้
ดู[ตัวเลือกการเข้าสู่ระบบและการแก้ปัญหา QR code](docs/CLI.th.md#login-options-and-qr-help)

### 2. ดูห้องแชทและส่งข้อความ

```sh
line whoami     # ตรวจสอบบัญชีที่ลงชื่อเข้าใช้
line chats      # แสดงรายการห้องแชท
line messages   # เลือกห้องแชทและอ่านข้อความล่าสุด
line send       # เลือกผู้รับและพิมพ์ข้อความ
```

ในเทอร์มินัลแบบโต้ตอบ CLI จะถามข้อมูลที่ยังไม่ได้ระบุ ใช้ Ctrl-C เพื่อยกเลิก
หรือระบุชื่อห้องแชทโดยตรงตามตัวอย่างด้านล่าง

<a id="line-messaging-commands"></a>

## คำสั่งสำหรับข้อความ LINE

| สิ่งที่ต้องการทำ | คำสั่ง |
| --- | --- |
| ค้นหาเพื่อน | `line contacts --search "Alice"` |
| ค้นหาบุคคลด้วย user ID | `line contacts --mid USER_MID` |
| ค้นหาห้องแชทและกลุ่ม | `line chats --search "Family"` |
| อ่านข้อความล่าสุด | `line messages "Alice" --limit 20` |
| ส่งข้อความ | `line send "Alice" --text "Hello!"` |
| ส่งไฟล์ | `line send "Alice" --file ./report.pdf` |
| ดาวน์โหลดรูปภาพ วิดีโอ เสียง หรือไฟล์ | `line download` |
| เพิ่มหรือลบการแสดงความรู้สึก | `line react` |
| ยกเลิกข้อความที่คุณส่ง | `line unsend` |
| รับเหตุการณ์แบบเรียลไทม์ | `line watch` |
| ตรวจสอบที่เก็บเซสชันในเครื่อง | `line auth status --check` |
| ลบเซสชันที่บันทึกไว้ในเครื่อง | `line logout` |

แทนที่ชื่อในตัวอย่างด้วยชื่อเพื่อนหรือห้องแชทของคุณ ชื่อต้องตรงทั้งหมดและไม่ซ้ำกัน
โดยไม่แยกตัวพิมพ์เล็กและใหญ่ ชื่อที่มีช่องว่างต้องใส่เครื่องหมายคำพูด
หากชื่อซ้ำ ให้ใช้ `line chats --search "Alice" --show-ids` แล้วระบุ chat ID แบบเต็ม

ใช้ `line COMMAND --help` เพื่อดูตัวเลือก คำสั่งดาวน์โหลด แสดงความรู้สึก และยกเลิกข้อความ
รองรับการเลือกแบบโต้ตอบด้วย ดู[คู่มือการเลือกห้องแชท](docs/CLI.th.md#find-chats-and-people)

### ตอบกลับข้อความ LINE และดาวน์โหลดไฟล์แนบ

หา ID ด้วย `--show-ids` แล้วแทนที่ `MESSAGE_ID` ในตัวอย่าง:

```sh
line messages "Alice" --show-ids
line send "Alice" --text "Sounds good" --reply-to MESSAGE_ID
line react "Alice" --message MESSAGE_ID --reaction love
line download "Alice" --message MESSAGE_ID --output ./received.pdf
```

สำหรับการดาวน์โหลด ให้เลือก ID ของข้อความที่มีไฟล์แนบและชื่อไฟล์ที่เหมาะสม
ไฟล์ที่มีอยู่แล้วจะไม่ถูกเขียนทับ
ดู[ตัวเลือกเพิ่มเติมสำหรับการแสดงความรู้สึก ยกเลิกข้อความ และดาวน์โหลด](docs/CLI.th.md#download-or-change-a-message)

<a id="automate-line-with-json-and-shell-scripts"></a>

## ทำงานอัตโนมัติบน LINE ด้วย JSON และ shell script

เพิ่ม `--json` เพื่อส่งข้อมูลแบบมีโครงสร้างออกทาง stdout ส่วนข้อความวินิจฉัยจะออกทาง stderr
`--json` และ `--stdin` ปิดการถามข้อมูลแบบโต้ตอบ จึงต้องระบุอาร์กิวเมนต์ที่จำเป็นทั้งหมด
ใช้ chat ID แบบเต็มในสคริปต์ เพราะชื่ออาจเปลี่ยนได้

```sh
line chats --search "Family" --json
line messages CHAT_ID --limit 20 --json
line send CHAT_ID --stdin --json < message.txt
line watch --json > events.ndjson
```

หา `CHAT_ID` จาก `line chats --json` โดย `--stdin` รับข้อความหลายบรรทัดจากไฟล์หรือ pipe ได้
`watch` ส่ง JSON หนึ่งรายการต่อบรรทัด (NDJSON) และเริ่มต่อจาก checkpoint ที่บันทึกไว้
โปรแกรมที่รับข้อมูลควรใช้ revision เพื่อตัดเหตุการณ์ซ้ำ ข้อความและเหตุการณ์ที่ส่งออก
เป็นข้อมูลส่วนตัว ควรเก็บรักษาอย่างเหมาะสม

การเปลี่ยนแปลงข้อมูลฝั่งเซิร์ฟเวอร์จะพยายามเพียงครั้งเดียวและไม่ลองซ้ำโดยอัตโนมัติ
หากไม่ได้รับการตอบกลับจากการส่งข้อความ อัปโหลด แสดงความรู้สึก หรือยกเลิกข้อความ
ให้ตรวจสอบใน LINE ก่อนทำซ้ำเพื่อหลีกเลี่ยงการทำงานซ้ำ

ดูรายละเอียดการเชื่อมต่อใน[ฟิลด์ JSON และรหัสสถานะการจบโปรแกรม](docs/CLI.th.md#json-output-and-exit-codes)
และ[การรับเหตุการณ์แบบเรียลไทม์](docs/CLI.th.md#watch-new-events)

## การเข้ารหัส Letter Sealing และความปลอดภัยของเซสชัน

- **การเข้ารหัส:** ใช้ Letter Sealing เมื่อรองรับ คีย์ที่หายไปหรือไม่ถูกต้องและความผิดพลาดในการรับส่งข้อมูล
  จะไม่ทำให้การส่งแบบเข้ารหัสเปลี่ยนเป็นข้อความธรรมดาโดยไม่แจ้งให้ทราบ
  ผลลัพธ์การส่งแบบ JSON ระบุสถานะการเข้ารหัส
- **ที่เก็บข้อมูล:** macOS ใช้ Keychain ส่วน Linux ใช้ไฟล์เซสชัน AES-GCM โดยเก็บคีย์ใน
  Secret Service และ Windows ใช้ DPAPI ของผู้ใช้ปัจจุบัน Linux แบบ headless ใช้ที่เก็บข้อมูลแบบ host key
- **การกู้คืนเซสชัน:** แอ็กเซสโทเค็นจะรีเฟรชโดยอัตโนมัติเมื่อข้อมูลรับรองสำหรับรีเฟรชที่บันทึกไว้ยังใช้งานได้
  แม้หลังเริ่มโปรแกรมใหม่ หากรีเฟรชแล้วยังกู้คืนการเข้าถึงไม่ได้ หรือ LINE ยกเลิกเซสชัน ให้เข้าสู่ระบบอีกครั้ง

[ที่เก็บเซสชัน](docs/CLI.th.md#where-your-session-is-stored) ·
[การรีเฟรชโทเค็นและการกู้คืน](docs/CLI.th.md#token-refresh)

## ข้อจำกัด

- อ่านได้เฉพาะประวัติล่าสุด สูงสุด 100 ข้อความต่อครั้ง การอ่านจะไม่ทำเครื่องหมายว่าอ่านแล้ว
  และข้อความเก่าที่เข้ารหัสบางข้อความอาจอ่านไม่ได้
- การส่งไฟล์และดาวน์โหลดสื่อจำกัดที่ 20 MiB รูปภาพ วิดีโอ และเสียงที่ส่งด้วย `--file`
  จะแสดงเป็นไฟล์ทั่วไป ไม่รองรับการส่งสติกเกอร์หรือข้อความสื่อชนิดเฉพาะ
  และไม่สามารถดาวน์โหลดจาก URL สื่อภายนอกได้
- LINE CLI ใช้โปรโตคอลแบบเดียวกับไคลเอนต์ LINE สำหรับ Chrome การเปลี่ยนแปลงฝั่งเซิร์ฟเวอร์
  อาจส่งผลต่อความเข้ากันได้ และการทำงานบางส่วนยังต้องทดสอบกับระบบจริงเพิ่มเติม

## อัปเดต LINE CLI

หยุดคำสั่งและโปรเซสติดตามเหตุการณ์ที่กำลังทำงานก่อนอัปเกรด
ไม่รองรับการใช้เวอร์ชันเก่าและใหม่พร้อมกัน

```sh
line update --check   # ตรวจสอบรีลีสล่าสุดโดยไม่ติดตั้ง
line update           # อัปเดตเมื่อรองรับ หรือแสดงวิธีอัปเกรด
```

การติดตั้งแบบ standalone ด้วยตัวติดตั้งทางการบน macOS และ Linux อัปเดตได้โดยตรง
สำหรับ Homebrew ใช้ `brew upgrade line-cli` ส่วนการติดตั้งวิธีอื่นจะแสดงคำแนะนำการอัปเกรด
การตรวจสอบไม่ต้องเข้าสู่ระบบ LINE และจะทำเฉพาะเมื่อคุณรันคำสั่ง
ดู[รายละเอียดการอัปเดต](docs/CLI.th.md#updating-line-cli)

## เอกสารและความช่วยเหลือ

- [คู่มือ CLI](docs/CLI.th.md): วิธีใช้งานครบทุกคำสั่ง การแก้ไขปัญหา และการตั้งค่าเซิร์ฟเวอร์
- [การตรวจสอบโทเค็นและเซสชัน](TOKEN_SESSION.md): รายละเอียดการทำงานของการรีเฟรชและกู้คืน
- [GitHub Issues](https://github.com/kongesque/line-cli/issues): รายงานบั๊กและขอฟีเจอร์

เมื่อรายงานบั๊ก ให้ระบุผลของ `line version` ระบบปฏิบัติการ คำสั่ง และข้อความผิดพลาดที่ลบข้อมูลอ่อนไหวแล้ว
อย่าใส่รหัสผ่าน โทเค็น ค่าสำหรับเข้าสู่ระบบด้วย QR หรือข้อความส่วนตัว

<a id="build-and-contribute"></a>

## บิลด์และร่วมพัฒนา

การบิลด์จากซอร์สโค้ดต้องใช้ Git และ **Go 1.26+** บน macOS ต้องมี Xcode Command Line Tools
และ CGO เพื่อรองรับ Keychain ด้วย สำหรับ macOS หรือ Linux:

```sh
git clone https://github.com/kongesque/line-cli.git
cd line-cli
./install.sh
```

ทำตามคำแนะนำเกี่ยวกับ PATH ที่ตัวติดตั้งแสดง ดูคำสั่งสำหรับ Windows PowerShell ใน
[คู่มือบิลด์จากซอร์สโค้ด](docs/CLI.th.md#build-from-source) ข้อกำหนดที่เก็บข้อมูลบน Linux
ใช้กับการบิลด์จากซอร์สโค้ดด้วย

สำหรับการพัฒนาในเครื่อง:

```sh
./build.sh
./bin/line help
go test -race ./internal/... ./cmd/line ./pkg/line/... ./pkg/e2ee
go vet ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
```

ดูรายการตรวจสอบทั้งหมดและข้อกำหนดของโปรโตคอลใน[คู่มือผู้ร่วมพัฒนา](AGENTS.md)
CI ครอบคลุมทั้งสามแพลตฟอร์มและที่เก็บข้อมูลรับรองของระบบปฏิบัติการ
การทดสอบใช้ข้อมูลสังเคราะห์และ API จำลอง การทดสอบกับ LINE จริงต้องได้รับอนุญาตอย่างชัดเจน

## สัญญาอนุญาตและที่มา

LINE CLI ไม่มีความเกี่ยวข้องและไม่ได้รับการรับรองจาก LINE โครงการนี้พัฒนาต่อยอดจาก
[beeper/line](https://github.com/beeper/line) โดยโค้ดโปรโตคอลและการเข้ารหัสที่ใช้ร่วมกัน
ยังคงประกาศลิขสิทธิ์และที่มาของโครงการต้นทางไว้ ส่วน Matrix connector และชุดระบบ
สำหรับนำ Beeper ไปใช้งานจริงไม่ได้รวมอยู่ในโครงการนี้

เผยแพร่ภายใต้ [สัญญาอนุญาต MIT](LICENSE)
