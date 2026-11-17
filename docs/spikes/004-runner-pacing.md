# Spike 004 — Runner pacing: `time.Ticker` davranışı

- **Tarih:** 2026-11-17
- **Durum:** Tamamlandı
- **İlgili:** [ADR-0004](../architecture/adr/0004-simulation-time-model.md), Phase 2

## Soru
Gerçek zamanlı runner `time.Ticker` ile tick üretecek. (1) Uzun sürede kayma (drift) birikir mi?
(2) Tüketici bir tick'i geç işlerse kaçan tick'ler kuyruklanır mı, düşürülür mü? (3) Jitter ne kadar?

## Yöntem
Bağımsız küçük bir Go programı (Go 1.26.2, darwin/arm64, Apple Silicon dizüstü; scratch dizininde, repoya
dahil değil). Ardışık tick'ler arasındaki wall-clock aralıkları ölçüldü; "dropped" = `wall / period − alınan tick`.

## Sonuçlar (gerçek ölçüm)

| Senaryo | Tick | Wall | Düşen | Ortalama aralık | σ | p99 | max |
|---|---|---|---|---|---|---|---|
| 500 ms, normal tüketici | 10 | 5.001 s | 0 | 500.11 ms | 0.53 ms | 501.07 | 501.07 |
| 10 ms, normal tüketici | 300 | 3.001 s | 0 | 10.00 ms | 0.43 ms | 10.99 | 11.12 |
| 10 ms, her 10. tick 35 ms sürüyor | 300 | 3.617 s | 61 | 11.94 ms | 8.11 ms | 36.07 | 36.07 |
| 500 ms, her 5. tick 750 ms sürüyor | 10 | 5.752 s | 1 | 500.11 ms | 112 ms | 751.07 | 751.07 |

## Bulgular
1. **Kayma birikmiyor:** ticker başlangıç anına göre sabitlenir; 300 tick sonunda wall = 300 × period (±1 ms).
2. **Kaçan tick'ler düşürülür, kuyruklanmaz:** yavaş tüketicide "yetişme patlaması" olmaz; kaybedilen tick'ler kalıcıdır.
3. **Jitter düşük:** normal yükte σ ≈ 0.5 ms; 500 ms periyot için ihmal edilebilir.
4. Engine `Step()` maliyeti mikro saniye mertebesinde olacağından düşme pratikte yalnızca GC duraklaması,
   aşırı yük veya makinenin uykuya geçmesiyle oluşur.

## Karar
- ADR-0004 korunur: **catch-up yok**. Sim zamanı yalnızca işlenen tick'lerle ilerler; sayaçlar sim zamanına göre
  integre edildiğinden fiziksel tutarlılık bozulmaz (yalnızca wall-clock'un gerisinde kalır).
- Runner, düşen tick sayısını (`wall_elapsed / dt − ticks`) ve son adım süresini `Stats()` ile raporlar;
  Phase 8 bunları metriğe çevirir. Dizüstü uyku gibi büyük boşluklarda patlama olmaması bilinçli tercih.
- Runner testleri değerleri değil yalnızca yaşam döngüsünü ve sıralamayı doğrular; değer testleri
  wall-clock'suz `Engine.Step` ile yapılır (zamanlamaya duyarlı/flaky test yok).
