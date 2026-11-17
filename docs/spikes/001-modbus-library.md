# Spike 001 — Go Modbus TCP Server Library Research

- **Tarih:** 2026-11-17
- **Durum:** Tamamlandı
- **İlgili ADR:** [ADR-0001](../architecture/adr/0001-modbus-server-implementation.md)
- **PoC:** `spikes/poc/` (ayrı Go modülü, ana koda dahil değil)

## 1. Soru

Simülatörün Modbus TCP **server** katmanı için hangi Go kütüphanesi (veya kendi implementasyonumuz) kullanılmalı? Kriterler: server desteği, 4 veri alanı, özel datastore / write handler, eşzamanlılık güvenliği, bağlantı yönetimi, Unit ID, lisans, bakım durumu, spec uyumu.

## 2. Doğrulama yöntemi

Sürüm ve tarih bilgileri **uydurulmadı**; aşağıdaki kaynaklardan 2026-11-17 tarihinde çekildi:

- `https://proxy.golang.org/<module>/@latest` (son sürüm ve yayın zamanı)
- `https://api.github.com/repos/<owner>/<repo>` (yıldız, açık issue, son push, archived, lisans)
- `go mod download` ile kaynak kodu indirilip server kodu doğrudan okundu.
- simonvetter/modbus, gerçek TCP üzerinden PoC testleriyle doğrulandı.

> Not: GitHub yıldız/issue sayıları anlık değerlerdir ve kalite göstergesi olarak tek başına yeterli değildir.

## 3. Adaylar

| Modül | Son sürüm (proxy) | Yayın | Son push | ★ | Açık issue | Lisans | Server? |
|---|---|---|---|---|---|---|---|
| `github.com/simonvetter/modbus` | v1.6.4 | 2025-11-06 | 2026-07-28 | 457 | 29 | MIT | ✅ handler tabanlı |
| `github.com/aldas/go-modbus-client` | v0.6.0 | 2026-08-20 | 2026-08-25 | 36 | 0 | Apache-2.0 | ✅ paket seviyesinde (`server/`) |
| `github.com/tbrandon/mbserver` | pseudo-version (tag yok) | 2023-12-08 | 2024-07-22 | 281 | 14 | MIT | ✅ sabit bellek dizileri |
| `github.com/things-go/go-modbus` | pseudo-version | 2023-03-04 | 2023-03-04 | 166 | 7 | MIT | ✅ — **repo archived** |
| `github.com/grid-x/modbus` | v1.5.1 | 2026-09-04 | 2026-10-16 | 150 | 20 | BSD-3-Clause | ❌ yalnızca client |
| `github.com/goburrow/modbus` | v0.1.0 | 2018-09-19 | — | — | — | BSD | ❌ yalnızca client, bakımsız |

## 4. Detaylı değerlendirme

### 4.1 simonvetter/modbus v1.6.4 (PoC ile test edildi)

API: `modbus.NewServer(*ServerConfiguration, RequestHandler)`; handler arayüzü 4 metottan oluşur
(`HandleCoils`, `HandleDiscreteInputs`, `HandleHoldingRegisters`, `HandleInputRegisters`). Her istek
`UnitId`, `Addr`, `Quantity`, `IsWrite`, `Args`, `ClientAddr` taşır. Datastore tamamen bize aittir —
mimarimize (Virtual PLC Memory) doğrudan uyar.

Artılar:
- FC01, 02, 03, 04, 05, 06, 15, 16 destekli; desteklenmeyen FC → exception 01 (doğrulandı).
- Unit ID handler'a iletilir; transaction ID ve unit ID yanıtta doğru yankılanır (doğrulandı).
- Her client ayrı goroutine; `MaxClients`, idle `Timeout`; TLS (Modbus Security) desteği.
- Tek runtime bağımlılığı: `github.com/goburrow/serial` (RTU için, TCP'de kullanılmıyor).

Eksiler / PoC'de **doğrulanan** sapmalar:
1. **Spec sapması:** `quantity == 0`, register için `quantity > 125`, FC05 değeri `0xFF00/0x0000` dışında
   olduğunda Modbus spec'i **exception 03 (Illegal Data Value)** ister. Kütüphane bunları
   `ErrProtocolError` sayıp **TCP bağlantısını kapatıyor** (`TestExceptionsRawFrames`, 3 alt test).
2. Exception eşlemesi `switch err` (eşitlik) ile yapılıyor; `fmt.Errorf("%w")` ile sarılmış hatalar
   exception 04'e düşer. Handler'lar sentinel'leri sarmadan dönmek zorunda.
3. `Stop()` handler goroutine'lerinin bitmesini **beklemiyor** (WaitGroup yok); bağlantıları kapatıp hemen döner.
   PoC'de goroutine'ler ~ms içinde sönümlendi, ama graceful shutdown garantisi yok, `context` desteği yok.
4. Logger `*log.Logger`; `slog` entegrasyonu yok. Bağlantı sayısı / istek sayısı için hook yok (Phase 8 metrikleri).
5. `go.mod` → `go 1.16`; kod gofmt dışı biçimlenmiş (tab hizalamaları), ama okunabilir ve küçük (~900 satır server).

### 4.2 aldas/go-modbus-client v0.6.0 (`server` paketi)

- `server.Server{}.ListenAndServe(ctx, addr, handler)` + `Shutdown(ctx)` → `http.Server` benzeri, context-aware.
- Handler `Handle(ctx, packet.Request) (packet.Response, error)` — ham paket tipi üzerinde type-switch yapmak
  ve yanıt paketini kendimiz kurmak gerekiyor; datastore yardımcı yapısı yok.
- `packet` paketi ayrıştırma + yanıt oluşturmayı kapsamlı biçimde sağlıyor (FC01–06, 15, 16, 17, 23).
- Aktif bakım (CHANGELOG'da 2026 sürümleri, Go 1.26 CI). Topluluk küçük (36★).
- Kütüphanenin odağı **client/poller**; server ikincil. `go.mod`'da testify (test bağımlılığı) geliyor.
- PoC ile test edilmedi (kaynak kodu okundu). Değerlendirme kaynak incelemesine dayanır.

### 4.3 tbrandon/mbserver — **elendi**
Tag yok, `go.mod` yok, sabit 65536'lık bellek dizileri ve `RegisterFunctionHandler` ile özelleştirme;
Unit ID bazlı ayrım ve yazma doğrulaması için framework'ü fiilen yeniden yazmak gerekir. Son push 2024-07.

### 4.4 things-go/go-modbus — **elendi**
GitHub'da `archived: true`.

### 4.5 grid-x/modbus — **server değil**, ancak test client'ı olarak seçildi
Aktif bakım (v1.5.1, 2026-09), BSD-3, context destekli client. Server implementasyonundan **bağımsız** bir
kod tabanı olduğu için interop testlerinde referans client olarak kullanıldı.

### 4.6 Seçenek C — Go stdlib ile kendi Modbus TCP server katmanımız
Modbus TCP framing basittir: 7 baytlık MBAP header + PDU (≤253 bayt). FC01–06/15/16 için
tahmini kapsam 300–450 satır kod + testler.

Artılar: spec'e birebir uyum (exception 03), `errors.Is`, `context` + `sync.WaitGroup` ile gerçek graceful
shutdown, `log/slog`, Phase 8 için metrik hook'ları (client sayısı, istek/hata sayacı), sıfır runtime bağımlılığı.
Eksiler: protokol hataları riski bizde → interop + raw-frame + fuzz testleriyle azaltılır; ilk faz biraz daha uzun.

## 5. Sonuç ve öneri

**Öneri: Seçenek C** — `internal/modbus` altında stdlib tabanlı ince bir Modbus TCP server; arayüz
simonvetter'ın kanıtlanmış 4-metotlu handler modelinden esinlenir. Test-only bağımlılık:
`github.com/grid-x/modbus` (interop). Gerekçe ve alternatifler için bkz. ADR-0001.

**Yedek plan:** simonvetter/modbus v1.6.4 — PoC'de FC01–06, unit ID, eşzamanlılık ve shutdown açısından
çalıştığı kanıtlandı; yukarıdaki 3 spec sapması kabul edilirse Phase 1 doğrudan bununla da yapılabilir.

## 6. Doğrulanmamış / açık noktalar
- aldas server paketi gerçek trafikle test edilmedi.
- Harici GUI/CLI client'ları (mbpoll, modpoll, Modbus Poll, pymodbus) bu makinede kurulu değil; interop
  Go (grid-x) ve Python stdlib soket client'ı ile doğrulandı.
