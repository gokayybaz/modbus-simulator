# ADR-0003 — Komut coil'lerinin davranışı (edge vs level)

- **Durum:** Kabul edildi, Phase 4'te uygulandı (`internal/mapping/device.go`)
- **Tarih:** 2026-11-17

## Seçenekler
| | Level-triggered | Örnekleme ile edge | **Yazma-olayı ile edge + self-clearing** |
|---|---|---|---|
| Kısa pulse (1→0, < dt) | — | **Kaçırılır** (500 ms örnekleme) | Yakalanır (yazma anında latch) |
| Client çökerse | Komut asılı kalır | — | Sorun yok |
| Start=1 & Stop=1 | Belirsiz | Belirsiz | Öncelik kuralı (Stop kazanır) |
| Client'ın 0 yazması gerekir mi | Evet | Evet | Hayır |

## Karar
- **Start (C0), Stop (C1), Reset Fault (C2): "write-1-to-trigger, self-clearing".**
  Client 1 yazdığında PLC katmanı komutu latch eder ve engine kuyruğuna verir. Engine komutu bir sonraki tick'te tüketir;
  ardından PLC coil'i 0'a çeker. Client coil'i okuyarak "komut işlendi" el sıkışmasını görebilir (1 → 0).
  0 yazmak geçerli ve etkisizdir. İşlenmemiş komut varken tekrar 1 yazmak idempotenttir.
- **Emergency Stop (C3): level / maintained.** Gerçek E-stop butonu gibi: 1 olduğu sürece makine FAULT/E-STOP'ta tutulur.
  0'a çekmek (butonu bırakmak) tek başına yeterli değildir; FAULT'tan çıkış için ayrıca Reset gerekir.
- Aynı tick'te Start ve Stop: **Stop kazanır**. E-Stop aktifken Start reddedilir (yok sayılır, log'lanır).

## Gerekçe
Yazma olayı protokol katmanında senkron olarak görüldüğü için örnekleme periyodundan bağımsızdır; HMI buton
semantiğine (momentary push button) karşılık gelir ve bilinen PLC "command + handshake" kalıbıdır.
E-stop'un maintained olması güvenlik semantiğini taklit eder.
