# Spike 009 — YAML konfigürasyon modeli ve senaryo zamanlaması

- **Tarih:** 2026-11-17
- **Durum:** Tamamlandı
- **İlgili:** Phase 7, [Spike 006](006-yaml-library.md)

## Sorular
1. Hat başına proses parametreleri, gürültü ve arıza limitleri tam bir şema tekrarı gerektirmeden nasıl verilir?
2. Zaman tabanlı senaryo olayları determinizmi bozmadan nasıl uygulanır?
3. CLI bayrakları ile konfigürasyon dosyası nasıl birlikte yaşar?

## 1. Preset + kısmi override (PoC, scratch modül)
`go.yaml.in/yaml/v3` v3.0.6 ile doğrulandı:
- Ham `yaml.Node` olarak okunan `process:` bölümü, preset değerleriyle dolu bir struct'a strict decoder ile çözülünce
  **yalnızca yazılan alanlar değişiyor**: `{nominal_rpm: 30, throughput_tau: 12s}` → `RampRate` preset'teki 5 olarak kaldı ✅
- `time.Duration` alanları `"12s"`, `"250ms"` metinlerinden çözülüyor; `"twelve"` ve çıplak `5` reddediliyor ✅
- İç içe bölümde yazım hatası (`throughput_taux`) strict modda reddediliyor; düğümün dosyadaki satırı (`Node.Line`)
  hata mesajına eklenebiliyor ✅

**Karar:** Hat = `preset` + opsiyonel `setpoints`, `setpoint_limits`, `process`, `noise`, `fault_limits` override'ları.
Simülasyon struct'larına yaml etiketleri eklenir; çözme ve anlamsal doğrulama başlangıçta, hiçbir şey çalışmadan yapılır.

## 2. Senaryo zamanlaması
- Olay zamanı (`at_seconds`) tick'e çevrilir: `tick = round(at / dt) + 1` (olay, o anda başlayan adımın başında uygulanır).
- Runner her adımdan önce bir `CommandSource`'tan (senaryo oynatıcı) o tick'in komutlarını alır; komutlar Modbus
  yazmalarıyla aynı yoldan, aynı tick sınırında uygulanır → **aynı seed + aynı senaryo ⇒ birebir aynı sonuç**,
  `-time-scale`'den bağımsız.
- Senaryo bir arıza *sebebi* (`inject_cause`, Phase 5 HR8 ile aynı) veya doğrudan bir arıza (`inject_fault`,
  `TriggerFault`) enjekte edebilir; proje tanımındaki örnek (`inject_fault: melt_pressure_high`) birebir desteklenir.
- Senaryonun değiştirdiği setpoint, senaryo ve E-Stop değerleri holding register / coil imajına yansıtılır
  (Phase 4'te not edilen eksik): yayıncı, snapshot'ta değişen değerleri imaja yazar.

## 3. Bayrak + dosya önceliği
`varsayılan < konfigürasyon dosyası < açıkça verilen bayrak`. Dosyada `lines:` varsa `-lines` ile birlikte kullanılamaz
(belirsizlik → hata). Dosyadaki göreli yollar dosyanın dizinine göre çözülür. `-validate` her şeyi (dosya, haritalar,
senaryolar, preset'ler, override'lar) doğrulayıp çıkar.

## Neden yeni bir bağımlılık yok?
Aynı YAML kütüphanesi (Spike 006) yeterli; `Node` + strict decoder override ihtiyacını karşılıyor.
