# ADR-0001 — Modbus TCP server implementasyonu

- **Durum:** Kabul edildi (kullanıcı onayı: 2026-11-17)
- **Tarih:** 2026-11-17
- **Kaynak:** [Spike 001](../../spikes/001-modbus-library.md)

## Bağlam
Simülatör Modbus TCP server'dır. Gereksinimler: FC01–06 (sonra 15/16), doğru exception yanıtları,
yapılandırılabilir Unit ID, eşzamanlı client'lar, graceful shutdown, structured logging ve Phase 8 metrikleri
(bağlı client sayısı, istek/hata sayaçları). Proje kuralı: stdlib öncelikli, gereksiz bağımlılık yok.

## Seçenekler
1. **simonvetter/modbus v1.6.4** — PoC'de çalıştığı kanıtlandı; ancak quantity/FC05 değer hatalarında exception 03
   yerine bağlantıyı kapatıyor, `Stop()` goroutine'leri beklemiyor, `context`/`slog`/metrik hook'u yok, hata eşlemesi `==`.
2. **aldas/go-modbus-client v0.6.0 `server`** — context-aware, ama paket seviyesinde; datastore yok, topluluk küçük, odak client.
3. **Kendi stdlib implementasyonu** (`internal/modbus`).
4. tbrandon/mbserver, things-go/go-modbus — bakımsız/arşiv, elendi.

## Karar (öneri)
**Seçenek 3.** `internal/modbus` paketi: `net.Listener` + bağlantı başına goroutine + `sync.WaitGroup`,
`context` ile kapanış, MBAP framing, FC01–06 (Phase 1), FC15/16 (sonraki faz). Datastore'a erişim, simonvetter'ın
kanıtlanmış modeline benzeyen küçük bir arayüzle:

```go
type Handler interface {
    ReadCoils(unit uint8, addr, qty uint16) ([]bool, error)
    ReadDiscreteInputs(unit uint8, addr, qty uint16) ([]bool, error)
    ReadHoldingRegisters(unit uint8, addr, qty uint16) ([]uint16, error)
    ReadInputRegisters(unit uint8, addr, qty uint16) ([]uint16, error)
    WriteCoils(unit uint8, addr uint16, vals []bool) error
    WriteHoldingRegisters(unit uint8, addr uint16, vals []uint16) error
}
```
Hatalar `*modbus.Exception` (kod taşıyan tip) ile döner, `errors.As` ile eşlenir.

Test-only bağımlılık: `github.com/grid-x/modbus` v1.5.1 (BSD-3) interop client'ı olarak.

## Gerekçe
- Spec uyumu (exception 03) projenin "Modbus öğrenme / client test etme" hedefinin merkezinde.
- Graceful shutdown ve metrikler server içine hook gerektiriyor; harici kütüphanede fork gerekir.
- Protokol katmanı küçük (~300–450 satır), iyi tanımlanmış ve iyi test edilebilir.

## Sonuçlar
- (+) Sıfır runtime bağımlılığı, tam kontrol, öğretici kod tabanı.
- (−) Protokol hataları bizim sorumluluğumuzda → raw-frame tablo testleri, grid-x interop testleri, `go test -fuzz` ile ADU decoder.
- Geri dönüş yolu: Handler arayüzü simonvetter'ınkine yakın olduğundan gerekirse adaptörle kütüphaneye geçilebilir.
