# Day 4 — SDLC Fundamentals: Practice

**Ürün fikri:** *Vardiya* — 5–20 çalışanlı kafe/restoranlar için vardiya planlama ve puantaj uygulaması.
**Kapsam:** Web paneli (işletme sahibi) + mobil görünüm (çalışan). Vardiya oluştur, çalışana bildir, izin/değişim taleplerini yönet, ay sonunda çalışılan saatleri muhasebeye CSV olarak aktar.

---

## Task 1 — Case Study: Hangi model? → **Hybrid**

Seçim: **Hybrid** (puantaj/mevzuat tarafı Waterfall, planlama & UX tarafı Agile).

- **Ürünün iki farklı doğası var.** Vardiya planlama ekranı kullanıcıyla birlikte evrilecek (kullanım alışkanlıkları önceden bilinemez → Agile), ama puantaj çıktısı iş kanunu ve muhasebe formatına bağlı; kuralları baştan yazılabilir ve değişmez → o modül için Waterfall disiplini uygun.
- **Hata maliyeti asimetrik.** Vardiya ekranında yanlış buton yeri = bir sprintlik düzeltme. Puantajda yanlış fazla mesai hesabı = işletmeye yasal/parasal zarar. Yüksek maliyetli kısmı önden tam spesifikasyonla (SRS + onay) kilitlemek mantıklı.
- **Müşteri erişimi sınırlı ama var.** 3 pilot kafe ile 2 haftada bir demo yapılabilir; günlük müşteri temsilcisi (saf Agile'ın istediği) yok. Bu, sprint'li ama hafif ritüelli bir Hybrid'e işaret ediyor.
- **Küçük ekip, kısa süre.** 3 kişilik ekip; saf Waterfall'ın ürettiği doküman yükünü taşıyamaz, saf Agile'ın gerektirdiği otomasyon/olgunluk da yok. Hybrid, dokümantasyonu sadece riskli modülde zorunlu tutarak yükü dengeliyor.
- **Pazar belirsizliği orta seviyede.** "Kafeler vardiyayı WhatsApp'tan yönetiyor" varsayımı doğrulanmalı; erken sürüm çıkarıp geri bildirim almak şart. Waterfall'ın 4 ay sonra tek seferde teslim modeli bu doğrulamayı çok geciktirir.

**Kısaca:** Sabit ve pahalı-yanlış olan kısmı önden tasarla, belirsiz ve ucuz-yanlış olan kısmı iterasyonla keşfet.

---

## Task 2 — Phase Checklist (faz başına beklenen artifact'lar)

### 1. Planning / Discovery
- [ ] Vizyon ve kapsam dokümanı (1 sayfa: problem, hedef kullanıcı, "yapmayacaklarımız")
- [ ] Paydaş listesi (işletme sahibi, çalışan, muhasebeci)
- [ ] Basit iş gerekçesi: hedef 50 işletme × ₺X/ay, geliştirme maliyeti tahmini
- [ ] Yüksek seviye yol haritası (MVP → v1 → v1.5)
- [ ] Varsayım ve risk listesi (ilk hali)

### 2. Requirements / Analysis
- [ ] Ürün backlog'u (user story formatında) — Agile tarafı
- [ ] Puantaj modülü için **SRS**: fazla mesai, hafta tatili, gece vardiyası kuralları — Waterfall tarafı
- [ ] Her story için kabul kriterleri (Given/When/Then)
- [ ] 2 persona (İşletme sahibi Deniz, part-time barista Ece)
- [ ] Gereksinim izlenebilirlik matrisi (yalnızca puantaj modülü için)

### 3. Design
- [ ] Sistem mimarisi diyagramı (istemci / API / veritabanı / bildirim servisi)
- [ ] Veri modeli — ERD (Employee, Shift, Availability, LeaveRequest, TimeEntry)
- [ ] API sözleşmesi (OpenAPI/Swagger taslağı)
- [ ] Wireframe + tıklanabilir prototip (haftalık vardiya takvimi ekranı)
- [ ] NFR listesi: 20 kullanıcıya kadar <2sn yüklenme, KVKK uyumlu veri saklama
- [ ] Tehdit modeli taslağı (kimlik doğrulama, yetkilendirme: çalışan başkasının maaşını görmemeli)

### 4. Implementation
- [ ] Kaynak kod + repo kuralları (branch stratejisi, commit formatı)
- [ ] Code review kayıtları (PR'lar)
- [ ] Birim testleri (özellikle puantaj hesaplama fonksiyonları için)
- [ ] CI pipeline yapılandırması
- [ ] Veritabanı migration script'leri
- [ ] Changelog / sürüm notları taslağı

### 5. Testing
- [ ] Test planı (kapsam, ortamlar, çıkış kriterleri)
- [ ] Test senaryoları + puantaj için kenar durum tablosu (ay ortası işe giriş, gece 00:00'ı aşan vardiya, resmi tatil)
- [ ] Hata kayıtları (bug report) ve önem seviyeleri
- [ ] Regresyon test seti (otomatik)
- [ ] UAT onay formu — pilot kafenin imzası
- [ ] Muhasebe CSV çıktısının gerçek muhasebeciyle doğrulama raporu

### 6. Deployment
- [ ] Sürüm notları (release notes)
- [ ] Runbook: nasıl deploy edilir, nasıl geri alınır
- [ ] Rollback planı ve tetikleyici koşulları
- [ ] Ortam/altyapı yapılandırması (env değişkenleri, IaC)
- [ ] İzleme ve alarm kurulumu (hata oranı, API gecikmesi, başarısız bildirim)
- [ ] Kullanıcı kılavuzu / onboarding e-postası

### 7. Maintenance / Operations
- [ ] Destek ve SLA dokümanı (yanıt süresi taahhüdü)
- [ ] Olay (incident) kayıtları ve postmortem şablonu
- [ ] Kullanım analitiği raporu (aylık: aktif işletme, oluşturulan vardiya sayısı)
- [ ] Teknik borç kaydı
- [ ] Geri bildirimden beslenen iyileştirme backlog'u

---

## Task 3 — Risk Flag

**En riskli faz: Requirements / Analysis.**
Özellikle puantaj kurallarının çıkarılması. Nedeni: bu fazdaki bir hata sessizdir — kod çalışır, ekran açılır, CSV üretilir, ama rakam yanlıştır. Hata aylar sonra muhasebede ortaya çıkar; o noktada yanlış veri zaten üretilmiş, müşteri güveni ve muhtemelen yasal sorumluluk devrededir. Ayrıca gereksinim hatasının düzeltme maliyeti, kodlama fazındaki bir hataya göre kat kat yüksektir çünkü tasarım, kod, test ve üretilmiş verinin hepsi geriye dönük etkilenir.

**Mitigation (azaltma planı):**
1. **Uzman doğrulaması:** Puantaj kurallarını yazdıktan sonra bir mali müşavire ücretli 2 saatlik review yaptır; onaylı SRS'i referans belge olarak sakla.
2. **Örnekle spesifikasyon:** Soyut kural cümlesi yerine 15 gerçek senaryoyu tablo halinde yaz (girdi vardiyalar → beklenen çıktı saat/ücret). Bu tablo aynı zamanda test senaryosu olur.
3. **Erken ince dilim (thin vertical slice):** İlk 2 haftada tek bir kafenin gerçek geçen ay verisiyle uçtan uca puantaj üret; sonucu kafenin elle hesapladığı bordroyla karşılaştır.
4. **İzlenebilirlik:** Her puantaj kuralına ID ver (PAY-01, PAY-02...) ve kod + test dosyalarında bu ID'yi referansla; bir kural değişince etkilenen her yer 1 aramada bulunsun.
5. **Değişiklik kontrolü:** Puantaj modülünde gereksinim değişikliği ancak yazılı onayla girsin; koridor konuşmasıyla kural değişmesin.

---

## Task 4 — Teach-back Script (~1 dk 45 sn)

> "SDLC'yi merak ediyorsun ya —aslında bir kafe açmakla neredeyse aynı şey.
>
> Diyelim kafe açacaksın. Önce oturup düşünüyorsun: kimin için, nerede, ne kadar paran var? Buna **planlama** diyoruz.
>
> Sonra tam olarak ne istediğini listeliyorsun: 30 kişilik oturma alanı, espresso makinesi, kahvaltı da olsun. Bu **gereksinim analizi** — yazılımda da önce 'bu program tam olarak ne yapacak' sorusunu netleştiriyoruz.
>
> Ardından mimarla oturup planı çiziyorsun: mutfak nereye, priz nereye, tesisat nasıl geçecek. Bu **tasarım**. Yazılımda da kod yazmadan önce sistemin şeması çiziliyor; çünkü duvar örüldükten sonra prizin yerini değiştirmek pahalı.
>
> Sonra inşaat başlıyor: ustalar geliyor, iş yapılıyor. Bu **geliştirme**, yani asıl kod yazma kısmı. İnsanların 'yazılım' deyince aklına gelen tek şey bu, ama gördüğün gibi toplamın sadece bir parçası.
>
> Sonra açılıştan önce her şeyi deniyorsun: makine çalışıyor mu, su akıyor mu, sipariş sistemi doğru fiş basıyor mu? Bu **test**. Amaç, müşteri bulmadan önce hatayı biz bulalım.
>
> Sonra kapıyı açıyorsun — **yayına alma**. Ve iş burada bitmiyor: her gün bakım var, müşteri 'keşke şu da olsa' diyor, bozulan oluyor. Buna **bakım** diyoruz ve bir ürünün ömrünün en uzun kısmı budur.
>
> SDLC bu altı adımın adı, hepsi bu. Farklı çalışma biçimleri de var: bazı ekipler her adımı sırayla bitirip diğerine geçiyor — *Waterfall*. Bazıları küçük parçalar halinde ilerleyip her 2 haftada bir çalışan bir şey gösteriyor — *Agile*. Çoğu ekip de ikisini karıştırıyor.
>
> Tek cümleyle: SDLC, 'otur bir şeyler kodla' ile 'güvenilir bir ürün teslim et' arasındaki farkın adı."