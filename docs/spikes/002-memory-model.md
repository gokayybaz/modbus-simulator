# Spike 002 — Modbus Memory Model

- **Tarih:** 2026-11-17
- **Durum:** Tamamlandı
- **İlgili:** [Register Convention](../registers/convention.md), [ADR-0002](../architecture/adr/0002-register-convention.md), [ADR-0003](../architecture/adr/0003-command-coil-semantics.md)
- **PoC:** `spikes/poc/server.go`, `spikes/poc/poc_test.go`

## 1. Veri alanları

| Alan | Boyut | Erişim (protokol) | FC | Simülatördeki kullanım |
|---|---|---|---|---|
| Coils | 1 bit | R/W | 01, 05, 15 | Komutlar (Start/Stop/Reset/E-Stop) |
| Discrete Inputs | 1 bit | R | 02 | Durum bitleri (Running, Heating, Fault, alarm bitleri) |
| Holding Registers | 16 bit | R/W | 03, 06, 16 | Setpoint'ler, senaryo seçimi |
| Input Registers | 16 bit | R | 04 | Proses değerleri, sayaçlar |

Input Register ve Discrete Input'a **yazma fonksiyon kodu yoktur**; dolayısıyla "read-only proses değerine yazma"
yapısal olarak engellenir. Holding Register'larda ise her register için limit doğrulaması yapılır (geçersiz → 03).

## 2. Adresleme: PDU adresi vs gösterim adresi

- Protokol çerçevesindeki adres **sıfır tabanlıdır** (PDU address).
- `40001`, `30001` gibi gösterimler "Modicon convention" olup *alan + 1 tabanlı offset* belirtir:
  `30001` = Input Register PDU adres 0; `40011` = Holding Register PDU adres 10.
- **Karar:** Kod, YAML ve testlerde yalnızca `area + 0-based address` kullanılır. Dokümantasyon tablolarında
  bilgi amaçlı ek bir "Display" kolonu bulunur. Kodda 3xxxx/4xxxx sayısı **asla** kullanılmaz.
- PoC doğrulaması: Python ham soket client'ı `FC04 addr=0 qty=3` → `(2, 456, 1234)` döndü — ilk register PDU 0.

## 3. 16-bit dönüşümler

- Register içeriği her zaman big-endian (yüksek bayt önce) iletilir — Modbus spec'i gereği; konfigüre edilemez.
- `UINT16`: doğrudan. `INT16`: iki tümleyen (`int16(-5)` → `0xFFFB`, test edildi).
- Ölçekleme: `raw = round(value × scale)`. Aralık dışı değerler **satürasyon** ile kırpılır (UINT16: 0..65535),
  NaN → 0, ve çağırana `ok=false` döner (Phase 4'te log/alarm bayrağına bağlanacak). `TestScaleU16` ile doğrulandı.
- Negatif olabilecek büyüklükler (örn. vakum gauge basıncı, sıcaklık sapması) `INT16` olarak tanımlanmalı.

## 4. 32-bit dönüşümler ve word order

Modbus spec'i 32-bit tip tanımlamaz; üreticiler farklı sıralar kullanır:

| Ad | Register sırası | 123456 (0x0001E240) için reg[n], reg[n+1] |
|---|---|---|
| **ABCD** (big-endian, high word first) — **varsayılan** | yüksek word önce | `0x0001`, `0xE240` |
| CDAB (word-swapped) | düşük word önce | `0xE240`, `0x0001` |
| BADC / DCBA (byte-swapped) | nadir | — |

**Karar:** Varsayılan `ABCD`. Phase 4'te register başına `word_order: [abcd|cdab]` opsiyonu eklenir
(BADC/DCBA kapsam dışı — gerçek bir ihtiyaç doğarsa eklenir).
PoC doğrulaması: grid-x client ve Python `struct.unpack(">I")` / `(">f")` her ikisi de `123456` ve
`3.14159` değerlerini doğru çözdü (`TestThirtyTwoBitWordOrder`).

## 5. Atomiklik

- Tek bir okuma isteği içindeki register'lar, bellek tek `RWMutex` altında kopyalandığı için **tutarlıdır**.
  `TestConcurrentClients`: 16 client × 200 okuma, eşzamanlı yazıcı varken 2-register değerlerde yırtılma (torn read) yok;
  `-race` temiz.
- Ayrı isteklerle okunan değerler arasında tutarlılık **Modbus'ın doğası gereği garanti edilemez**.
  Bu nedenle 32-bit değerler tek istekte okunmalıdır (dokümante edilecek).
- Simülasyon her tick'te tek bir snapshot yayınlar; PLC belleği bu snapshot'ı tek lock altında uygular.

## 6. Exception davranışı (simülatör politikası)

| Kod | Ad | Ne zaman |
|---|---|---|
| 01 | Illegal Function | Desteklenmeyen FC (örn. 0x07, 0x2B, Phase 1'de 15/16 kapalıysa) |
| 02 | Illegal Data Address | `addr+qty` tanımlı bloğun dışına taşıyor (kısmi taşma dahil) |
| 03 | Illegal Data Value | `qty` 0 veya limit üstü (bit: 2000, reg: 125, FC16: 123, FC15: 1968); FC05 değeri ≠ FF00/0000; setpoint limit dışı; byte count uyumsuz |
| 04 | Server Device Failure | Beklenmeyen iç hata |
| 0B | Gateway Target Failed to Respond | Tanımsız Unit ID (Phase 6'da çoklu hat ile anlamlı) |

Spec'te (Modbus Application Protocol v1.1b3) her FC için doğrulama sırası: FC → quantity (03) → adres (02) → işlem (04).
PoC raw-frame testleri 01, 02, 03 (setpoint limiti) ve 0B'nin TCP üzerinde doğru döndüğünü gösterdi; quantity/FC05
değer hataları için seçilen kütüphanenin bağlantıyı kapattığı tespit edildi (bkz. Spike 001).

## 7. Unit ID

- Modbus TCP'de Unit ID genelde gateway arkasındaki cihazı seçmek için kullanılır; doğrudan TCP cihazları çoğunlukla
  `0xFF` veya `0` bekler/yoksayar.
- **Karar:** Yapılandırılabilir Unit ID (varsayılan 1). Tanımsız Unit ID → exception 0B.
  `accept_any_unit_id` seçeneği Phase 1'de config'e eklenir (bazı client'lar 0 veya 255 gönderir).

## 8. Bulgular özeti
- 4 alan + FC01–06 TCP üzerinden doğrulandı (grid-x client ve Python stdlib client).
- ABCD word order, ölçekleme, INT16 dönüşümü testle doğrulandı.
- Tek istek içi atomiklik `-race` altında doğrulandı.
