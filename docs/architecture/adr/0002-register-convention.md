# ADR-0002 — Register adresleme ve veri tipi convention'ı

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-17
- **Kaynak:** [Spike 002](../../spikes/002-memory-model.md), [Register Convention](../../registers/convention.md)

## Karar
1. Kod ve YAML'da adres = `(area, 0-based PDU address)`. 3xxxx/4xxxx gösterimleri yalnızca dokümantasyonda bilgi amaçlı.
2. 32-bit tipler (UINT32, INT32, FLOAT32) varsayılan **ABCD** (big-endian bayt, yüksek word önce). Register başına
   `word_order: cdab` opsiyonu Phase 4'te.
3. Ölçekleme `raw = round(eng × scale)`; satürasyon + "clamped" bayrağı; NaN → 0.
4. Proses değerleri → Input Register / Discrete Input (yazılamaz). Setpoint'ler → Holding Register (limit doğrulamalı, aksi 03).
   Komutlar → Coil.
5. Tanımlı bloklar dışına taşan istek → 02. Blok içindeki rezerve adresler 0 döner (client'ların blok okumalarını kolaylaştırır).
6. Tanımsız Unit ID → 0B (opsiyonel `accept_any_unit_id`).

## Gerekçe
ABCD, Modbus'ın kendi register içi big-endian kuralıyla tutarlı ve en yaygın varsayılan (Modicon / Schneider, birçok
SCADA aracı). Sıfır tabanlı adresleme off-by-one hatalarını kaynağında yok eder.
