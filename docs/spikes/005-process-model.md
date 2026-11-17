# Spike 005 — Proses modeli varsayımları ve katsayılar

- **Tarih:** 2026-11-17
- **Durum:** Tamamlandı (el hesabı; simülasyonla doğrulama sonuçları Phase 3 raporunda)
- **İlgili:** [Spike 003](003-simulation-engine.md), Phase 3

> Bu belgedeki katsayılar **mühendislik mertebesinde makul** seçilmiştir; gerçek bir tesisin verisiyle
> doğrulanmamıştır. Amaç nedensel ilişkileri doğru yönde ve doğru büyüklük sırasında göstermektir.

## 1. Referans ürün ve hat
- Ürün: **PE100 boru Ø110 × 10.0 mm (SDR 11)**, katı yoğunluk ρ = 955 kg/m³.
- Ekstrüder: Ø75 mm tek vida, nominal 100 rpm, 4.5 kg/h/rpm → **450 kg/h**.
- Çekici: nominal **2.5 m/min**.

**Kütle dengesi kontrolü:** kesit alanı A = ṁ / (ρ·v) = (450/60 kg/min) / (955 × 2.5 m/min) = 3.141×10⁻³ m².
Boru kesiti A = π·s·(OD − s) ⇒ s = (OD − √(OD² − 4A/π)) / 2 = **9.999 mm** ✓ (nominal 10.0 mm).

## 2. Model zinciri (her tick bu sırayla)

```
zone/die sıcaklıkları + rpm ─► Melt: T_melt, viskozite oranı η_rel
RPM_SP ─rampa─► rpm ─► ṁ = k·rpm ─lag(8 s)─► throughput
                         └─► motor gücü = P0·rpm/rpm_nom + SEC·ṁ·√η_rel ─► akım, yük %
throughput ─► zone proses ısısı (besleme bölgesi ısı çeker, son bölge/kalıp sürtünme ısısı alır)
ısıtıcı (PI) + proses ısısı − kayıp ─► zone/die sıcaklığı (enerji dengesi)
throughput, η_rel ─► basınç = P_nom·(ṁ/ṁ_nom)^0.4·η_rel ─ölü zaman 1 s + lag 2 s─► melt pressure
RUNNING ─► vakum pompası ─lag─► vakum ; throughput·(cp·ΔT + gizli ısı) / (su debisi·cp_su) ─lag─► su sıcaklığı
HaulOff_SP ─rampa─► hız ─► OD = kalibratör·(1 − vakum açığı çekmesi − su sıcaklığı çekmesi)
                             s = kütle dengesi(ṁ, v, OD) ─► boru üzerinde mesafe tabanlı taşıma ─► ölçüm cihazı (6 m)
güç = motor elektrik + ısıtıcılar + vakum pompası + çekici
```

## 3. Katsayılar ve gerekçe

| Model | Parametre | Değer | Gerekçe / el hesabı |
|---|---|---|---|
| Extruder | k | 4.5 kg/h/rpm | 75 mm vida, 100 rpm → 450 kg/h |
| | rampa | 5 rpm/s | 0→100 rpm 20 s |
| | debi gecikmesi | 8 s | eriyiğin vidada ilerlemesi |
| | SEC | 0.20 kWh/kg | HDPE tipik 0.18–0.25 → 90 kW mekanik |
| | yüksüz güç P0 | 4 kW @100 rpm | sürtünme/şanzıman |
| | nominal motor | 132 kW, 400 V, cosφ 0.88, η 0.94 | 94 kW mek → ~164 A |
| Heating | ısıl kapasite C | 42 kJ/K (zone), 30 kJ/K (die) | ~90 kg çelik × 0.46 kJ/kgK |
| | kayıp h | 36 W/K (zone), 25 W/K (die) | 12 kW ile ulaşılabilir max ≈ 350 °C |
| | ısıtıcı | 12 kW (zone), 8 kW (die) | |
| | PI | Kp = 1200 W/K, Ti = 300 s, anti-windup | τ_plant = C/h ≈ 1160 s; P-only 5 °C ofset bırakır → PI gerekli |
| | proses ısısı @nominal | zone1 −4 kW, zone2 −2 kW, zone3 +1.5 kW, die +0.5 kW | katı polimeri ısıtma (feed) vs. sürtünme ısısı (metering) |
| Melt | T_melt | lag₂₀ₛ(ort(zone3, die) + 0.1 °C/rpm) | sürtünme ısısı: 100 rpm → +10 °C; eriyik ısıl ataleti (ters basınç tepkisini önler) |
| | η_rel | exp(−0.02·(T_melt − 210)), [0.05, 20] ile sınırlı | HDPE: ~35 °C'de viskozite ×2 |
| | basınç | 220 bar @ 450 kg/h, η_rel = 1, akış üssü 0.4 | kafa basıncı tipik 150–300 bar; kayma incelmesi n ≈ 0.4 |
| | gecikme | ölü zaman 1 s + lag 2 s | |
| Vacuum | hedef | 400 mbar (atmosfer altı büyüklük) | kalibrasyon tankı tipik 200–600 mbar |
| | çekiş / sönüm | τ 10 s / 5 s | |
| Cooling | besleme suyu | 12 °C, 15 m³/h, tank τ 180 s | ısı yükü ṁ·(2.3·(T_melt−40) + 220) kJ/kg ≈ 77 kW → +4.4 °C ⇒ ~16.4 °C |
| | verim | (T_melt − T_su)/(T_melt − T_besleme) | 1'e yakın = iyi soğutma |
| Pipe | kalibratör | 110.0 mm (nominal çekme dahil) | |
| | vakum açığı çekmesi | vakum 0'da en çok %2 | sleeve'e temas yetersiz |
| | su sıcaklığı çekmesi | %0.03 / °C (16 °C referans) | sıcak çıkan boru sonradan daha çok çeker |
| | ölçüm cihazı mesafesi | 6 m | 2.5 m/min'de 2.4 dk taşıma |
| Güç | vakum pompası 5.5 kW, çekici 1.5 kW @nominal | |

## 4. Bilinçli basitleştirmeler
- Debi devirle doğrusal (basınç geri akışı yok); vida doluluğu/beslemesi ideal.
- Zone'lar arası ısıl kuplaj yok; ortam sıcaklığı sabit 20 °C.
- Eriyik yoğunluğu ayrı modellenmiyor; kütle dengesi katı yoğunlukla (son ürün) yapılıyor.
- Ovalite, eksen kayması (eccentricity), çekme oranı (draw-down) modellenmiyor.
- Taşıma gecikmesi borunun **hareket ettiği mesafeye** göre (sabit zaman değil): çekici durursa ölçüm donar.
- Ölçüm cihazında henüz boru yokken OD/et kalınlığı 0 okunur.
