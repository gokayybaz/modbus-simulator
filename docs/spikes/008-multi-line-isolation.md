# Spike 008 — Çoklu hat izolasyonu: Unit ID vs TCP endpoint

- **Tarih:** 2026-11-17
- **Durum:** Tamamlandı
- **İlgili:** Phase 6

## Soru
Üç hat aynı uygulamada çalışacak. Client doğru hattı nasıl seçmeli: aynı IP:port üzerinde **Unit ID** ile mi,
her hat için **ayrı TCP endpoint** (port) ile mi?

## Değerlendirme

| | A: Tek endpoint, hat başına Unit ID | B: Hat başına port | **C: İkisi de (konfigürasyonla)** |
|---|---|---|---|
| Gerçeğe benzerlik | Modbus TCP gateway arkasındaki cihazlar (RTU → TCP gateway) | Her hattın kendi PLC'si / IP'si (sahada en yaygın) | İkisi de |
| Client uyumu | Unit ID'yi cihaz başına ayarlayabilen client gerekir | Unit ID'yi önemsemeyen/sabit 1 veya 255 gönderen client'lar da çalışır | — |
| Port/firewall | Tek port | N port | — |
| Bağlantı sayısı | Tek bağlantıdan tüm hatlar okunabilir | Hat başına bağlantı | — |
| Kod maliyeti | Unit ID → hat yönlendiricisi | Server başına bir listener | Yönlendirici + endpoint gruplama |

## PoC (geçici test, repoya dahil değil) — gerçek TCP, kendi server'ımız + grid-x client
- Unit ID → `plc.Memory` yönlendiricisi; her hat farklı değer içeriyor.
- **Tek TCP bağlantısında Unit ID istekten isteğe değiştirildi** (1,2,3,1,2,3…, 30 istek): her yanıt doğru hattan ✅
- Unit 2'ye FC06 yazma yalnızca hat 2'nin belleğini değiştirdi (hat 1 ve 3: 0) ✅
- Tanımsız Unit 9 → `exception '11' (gateway target device failed to respond)` (0x0B) ✅
- 30 bağlantı (hat başına 10) × 300 okuma, eşzamanlı, `-race` temiz: 79 ms ✅

Doğrulanmamış (bu makinede araç yok): ticari SCADA/OPC sunucularının Unit ID davranışı. Genel bilgi olarak çoğu
Modbus TCP sürücüsü cihaz başına Unit/Slave ID ayarına izin verir; bazı client'lar sabit 0/255 gönderir — onlar için
B modu (veya tek hatta `-accept-any-unit`) kullanılmalıdır.

## Karar: C
- **Varsayılan `-isolation unit`:** tek endpoint (`-addr`), hat *i* için Unit ID = `-unit-id` + i − 1 (1, 2, 3).
- **`-isolation port`:** hat *i* `host:port + i − 1` üzerinde, her biri Unit ID = `-unit-id`
  (port 0 verilirse her hat işletim sisteminin seçtiği ayrı bir port alır — testler için).
- Her hat tamamen ayrık: kendi engine'i, runner goroutine'i, RNG seed'i, PLC belleği, register map örneği,
  komut kuyruğu, sayaçları ve alarmları. Hatlar arasında paylaşılan değiştirilebilir durum yok.
- Yönlendirici (`plant.Router`) yalnızca Unit ID → `modbus.Handler` eşlemesidir; protokol katmanı değişmez.
