# Spike 006 — YAML kütüphanesi

- **Tarih:** 2026-11-17
- **Durum:** Tamamlandı
- **İlgili:** Phase 4 (register map), Phase 7 (konfigürasyon)

## Soru
Register haritası (ve Phase 7'de tüm konfigürasyon) YAML olacak. Stdlib'de YAML yok; hangi kütüphane?

## Adaylar (proxy.golang.org + GitHub API, 2026-11-17)

| Modül | Son sürüm | Yayın | Repo | ★ / açık issue | Lisans | Not |
|---|---|---|---|---|---|---|
| `go.yaml.in/yaml/v3` | v3.0.6 | 2026-10-13 | yaml/go-yaml (son push 2026-11-14) | 571 / 119 | MIT + Apache-2.0 | `gopkg.in/yaml.v3`'ün resmi halefi (YAML org), aynı API |
| `go.yaml.in/yaml/v4` | v4.0.0 | 2026-11-04 | aynı repo | — | aynı | **2 haftadan yeni** major sürüm → şimdilik değil |
| `github.com/goccy/go-yaml` | v1.19.2 | 2026-01-08 | son push 2026-04-11 | 2249 / 277 | MIT | Zengin hata çıktısı; v1 değil (API değişebilir) |
| `sigs.k8s.io/yaml` | v1.6.0 | 2025-07-24 | | 355 / 8 | (NOASSERTION) | YAML→JSON sarmalayıcı; strict mod JSON tag'leriyle |
| `gopkg.in/yaml.v3` | v3.0.1 | 2022-05-27 | go-yaml/yaml **archived** (2025-04-01) | | | Bakımsız |

## PoC (scratch, repoya dahil değil)
`yaml.NewDecoder(...).KnownFields(true)` ile:
- Bilinmeyen alan → `line 4: field typo_field not found in type main.Field` ✅
- `address: 70000` (uint16) → `line 2: cannot unmarshal !!int 70000 into uint16` ✅
- `address: one` → `line 2: cannot unmarshal !!str one into uint16` ✅
- `min: 0` → `*float64` ile "verilmedi" ve "0" ayrımı yapılabiliyor ✅
- Modülün `go.mod`'unda hiçbir `require` yok → **sıfır transitive bağımlılık**.

## Karar
**`go.yaml.in/yaml/v3` v3.0.6.** Strict decoding satır numaralı hata veriyor, bağımlılığı yok, bilinen API, aktif bakım.
v4, yayından birkaç hafta sonra (Phase 7'de) yeniden değerlendirilebilir.
Anlamsal doğrulama (adres çakışması, tip/ölçek, bilinmeyen kaynak) kütüphaneden bağımsız olarak `internal/mapping`'de yapılır.
