# ADR-0004 — Simülasyon zaman modeli ve determinizm

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-17
- **Kaynak:** [Spike 003](../../spikes/003-simulation-engine.md)

## Karar
1. Sabit timestep (`dt`, varsayılan 500 ms). `simTime = tick × dt`. Engine wall clock okumaz.
2. Gerçek zaman pacing'i ayrı bir runner (`time.Ticker`) yapar; düşen tick'ler telafi edilmez, gecikme metrik olarak raporlanır.
3. Komutlar yalnızca tick sınırında uygulanır.
4. RNG: `math/rand/v2` PCG, hat/alt sistem başına ayrı akış; gürültü yalnızca ölçümlere uygulanır.
5. Dinamikler: kesin ayrıklaştırılmış first-order lag, halka tamponlu dead-time, rate limiter.

## Sonuçlar
- Aynı seed + aynı (tick, komut) dizisi ⇒ birebir aynı çıktı (PoC'de doğrulandı).
- Testler gerçek zamanı beklemeden saatlerce proses zamanı simüle edebilir.
- Hızlandırılmış zaman (ör. 10×) runner periyodunu değiştirerek ileride eklenebilir; engine değişmez.
