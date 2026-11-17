# Ekstrüzyon Dashboard

Collector'ın TimescaleDB tablolarını (`machine_readings`, `machine_events`) düzenli aralıklarla sorgulayan
React arayüzü + Go JSON API. Tek konteyner (distroless), collector compose dosyasına `dashboard` servisi olarak eklidir.

## Çalıştırma
```bash
cd ../collector
docker compose up -d --build dashboard
```
Arayüz: <http://127.0.0.1:8080> · Port: `DASHBOARD_PORT=9090 docker compose up -d dashboard`

Collector varsayılan olarak 3 hattı (Unit ID 1,2,3) okur; tüm hatlar üst bardaki hat seçicide görünür.

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `DATABASE_URL` | compose'da `db` servisi | TimescaleDB bağlantısı |
| `POLL_INTERVAL` (`DASHBOARD_POLL_INTERVAL`) | `1s` | arayüzün canlı sayfa yenileme aralığı; hat 5× bu süre veri almazsa "gecikmeli" sayılır |
| `STATIC_DIR` | `/web` | derlenmiş React dosyaları |
| `LISTEN_ADDR` | `:8080` | |

## Sayfalar
- **Canlı İzleme** — durum, animasyonlu proses şeması (vida/çekici/boru hareketi canlı devir ve hıza bağlı; alarm veren
  ünite yanıp söner), basınç/motor yükü/vakum/su göstergeleri (uyarı–trip bantları), KPI + 3 dk sparkline,
  zon sıcaklık profili (SP ve uyarı bandıyla), alarm lambaları, olay akışı.
- **Trendler** — 5 dk…7 gün; tek birim/tek eksenli senkron grafikler (ortak crosshair ve zoom), FAULT/ısınma/durma bantları,
  uyarı ve trip eşikleri. Uzun aralıklar ≤ 720 noktaya kovalanır.
- **Kök Sebep Analizi** — arıza seçilir, geriye bakış penceresi ayarlanır:
  - **Olay zinciri:** setpoint değişiklikleri → sapma başlangıçları → uyarı bitleri → trip → sonuçlar (T− duvar ve sim. süreleri).
  - **Hipotezler:** bilgi tabanındaki her sebep, gözlenen belirtilerle puanlanır (yön eşleşmesi, çelişki, ana belirtinin ilk sapan
    sinyal olması). Kanıt listesi ✓/✗ ile gösterilir.
  - **Ishikawa (6M)**, **5 Neden**, **önerilen aksiyonlar**, **sapma ısı haritası** (z-skoru), baseline ±3σ bantlı sinyal grafikleri,
    sıralanabilir sinyal tablosu (sapma, öncülük, trip sinyali ile korelasyon).
  - Simülatörün sebep register'ı (HR8) puanlamaya **girmez**; yalnızca çıkarımı doğrulamak için "referans" rozeti olarak gösterilir.
- **Performans & Olaylar** — kullanılabilirlik, MTBF, MTTR, üretim, enerji ve özgül enerji (kWh/kg, simülasyon zamanıyla),
  durum zaman çizelgesi, arıza Pareto, filtrelenebilir olay günlüğü.

## RCA yöntemi (`internal/rca`)
1. Trip öncesi kesintisiz FAULT-dışı segment alınır; ilk %30'u baseline (ortalama, σ — sensör gürültü tabanıyla alttan sınırlı).
2. Her sinyal için z-skoru; **başlangıç** = tripe kadar ≥ 2.5σ'da kalan ve son okumada ≥ 3σ olan en erken örnek.
3. Fiziksel aralık dışı okuma (ör. 999.9 °C) sensör arızası belirtisi sayılır.
4. Trip sinyali iki okuma arasında limiti geçtiyse (kaba örnekleme) sapma arıza kodundan **çıkarım** olarak işaretlenir.
5. Okuma başına simülasyon süresi > 10 s ise arayüz "düşük çözünürlük" uyarısı verir — sıralama için `POLL_INTERVAL`'i
   düşürün veya simülatörü daha küçük `-time-scale` ile çalıştırın.

## API
`/api/meta` (sinyal/alarm/arıza/sebep kataloğu) · `/api/units` · `/api/live?unit=1&window=180` ·
`/api/trend?unit=1&range=15m` · `/api/stats?unit=1&range=1h` · `/api/events?unit=1&range=24h` ·
`/api/faults?unit=1&range=168h` · `/api/rca?unit=1&ts=<RFC3339>&code=11&lookback=10m&post=1m` · `/api/health`

## Geliştirme
```bash
go test ./...                       # RCA testleri
go run ./cmd/dashboard              # API :8080 (DB localhost:5432), web/dist'i servis eder
cd web && npm install && npm run dev  # Vite :5173, /api → :8080 proxy
```
