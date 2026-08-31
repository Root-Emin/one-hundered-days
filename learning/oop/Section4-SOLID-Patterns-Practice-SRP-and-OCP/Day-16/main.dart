// ============================================================================
// GÜN 16 — SRP VE OCP  (Dart)
//
// Çalıştırmak için:  dart run gun16_srp_ve_ocp.dart
// veya dartpad.dev'e yapıştır.
//
// BÖLÜM 1 -> Kötü tasarım: tek sınıfta beş sorumluluk, üç switch
// BÖLÜM 2 -> Smell check: SRP ihlalini nasıl fark edersin
// BÖLÜM 3 -> SRP refactor: değişme sebeplerine göre bölmek
// BÖLÜM 4 -> OCP: switch yerine strateji
// BÖLÜM 5 -> Genişletme testi: kaç yeri değiştirdik?
// BÖLÜM 6 -> Nüanslar: SRP "küçük sınıf" değildir, OCP her yere uygulanmaz
// ============================================================================

// ############################################################################
//  ORTAK VERİ
// ############################################################################

class Money {
  final int kurus;
  const Money(this.kurus);
  static const zero = Money(0);

  factory Money.lira(num amount) => Money((amount * 100).round());

  double get lira => kurus / 100;

  Money operator +(Money o) => Money(kurus + o.kurus);
  Money operator -(Money o) => Money(kurus - o.kurus);
  Money operator *(num f) => Money((kurus * f).round());
  bool operator >(Money o) => kurus > o.kurus;
  bool operator >=(Money o) => kurus >= o.kurus;

  Money percent(int p) => Money((kurus * p) ~/ 100);

  @override
  bool operator ==(Object other) =>
      identical(this, other) || (other is Money && other.kurus == kurus);

  @override
  int get hashCode => kurus.hashCode;

  @override
  String toString() => '${lira.toStringAsFixed(2)} ₺';
}

enum Region { domestic, neighboring, overseas }

/// Bölüm 1'deki kötü tasarımın switch'leri bu enum üzerinde.
enum CustomerType { individual, corporate, vip, student }

class OrderLine {
  final String product;
  final Money unitPrice;
  final int quantity;

  const OrderLine({
    required this.product,
    required this.unitPrice,
    required this.quantity,
  });

  Money get subtotal => unitPrice * quantity;
}

class Order {
  final String id;
  final String customerName;
  final String email;
  final List<OrderLine> lines;
  final Region region;
  final CustomerType customerType; // sadece KÖTÜ tasarım için

  const Order({
    required this.id,
    required this.customerName,
    required this.email,
    required this.lines,
    required this.region,
    this.customerType = CustomerType.individual,
  });

  Money get subtotal => lines.fold(Money.zero, (sum, l) => sum + l.subtotal);
}

// ############################################################################
//
//  BÖLÜM 1 — KÖTÜ TASARIM
//
//  Bu sınıfı DEĞİŞTİRMEYE ZORLAYACAK beş ayrı taraf var:
//
//    1. Muhasebe      -> KDV oranı değişti
//    2. Pazarlama     -> yeni müşteri tipi, yeni indirim kuralı
//    3. Lojistik      -> kargo fiyatları / bölge tanımları değişti
//    4. Hukuk         -> fatura metninde zorunlu ibare
//    5. Bilgi işlem   -> veritabanı veya e-posta sağlayıcısı değişti
//
//  Beş farklı ekip, tek dosya. Aynı hafta üçü birden değişiklik
//  isterse üçü birbirine çarpar.
//
//  "Bir sınıfın tek bir değişme sebebi olmalı" cümlesinin anlamı bu.
//  Metot sayısı değil, DEĞİŞMEYİ TETİKLEYEN TARAF sayısı.
//
// ############################################################################

class OrderProcessor {
  // ---- Sorumluluk 1: fiyat hesabı (muhasebe + pazarlama + lojistik) ----

  /// SWITCH #1 — müşteri tipi üzerinde
  Money calculateDiscount(Order order) {
    switch (order.customerType) {
      case CustomerType.individual:
        return Money.zero;
      case CustomerType.corporate:
        return order.subtotal.percent(10);
      case CustomerType.vip:
        return order.subtotal.percent(20);
      default:
        // Yeni bir tip eklenirse sessizce buraya düşer.
        return Money.zero;
    }
  }

  /// SWITCH #2 — AYNI enum üzerinde, başka bir metotta
  int loyaltyPoints(Order order) {
    switch (order.customerType) {
      case CustomerType.individual:
        return order.subtotal.kurus ~/ 10000;
      case CustomerType.corporate:
        return order.subtotal.kurus ~/ 5000;
      case CustomerType.vip:
        return order.subtotal.kurus ~/ 2000;
      default:
        return 0;
    }
  }

  /// SWITCH #3 — yine AYNI enum
  String badgeLabel(Order order) {
    switch (order.customerType) {
      case CustomerType.individual:
        return 'Bireysel';
      case CustomerType.corporate:
        return 'Kurumsal';
      case CustomerType.vip:
        return 'VIP';
      default:
        return 'Bilinmiyor';
    }
  }

  /// SWITCH #4 — bölge üzerinde
  Money calculateShipping(Order order) {
    switch (order.region) {
      case Region.domestic:
        return Money.lira(49.90);
      case Region.neighboring:
        return Money.lira(120);
      case Region.overseas:
        return Money.lira(340);
    }
  }

  Money calculateTax(Money amount) => amount.percent(20); // muhasebe

  Money calculateTotal(Order order) {
    final indirimli = order.subtotal - calculateDiscount(order);
    return indirimli + calculateTax(indirimli) + calculateShipping(order);
  }

  // ---- Sorumluluk 2: fatura metni (hukuk) ----

  /// SWITCH #5 — format üzerinde
  String formatInvoice(Order order, String format) {
    switch (format) {
      case 'text':
        return 'FATURA ${order.id}\n'
            '${order.customerName} (${badgeLabel(order)})\n'
            'Toplam: ${calculateTotal(order)}';
      case 'csv':
        return '${order.id},${order.customerName},${calculateTotal(order).lira}';
      default:
        return order.id;
    }
  }

  // ---- Sorumluluk 3: kayıt (bilgi işlem) ----
  void saveToDatabase(Order order) {
    print('    [KÖTÜ] ${order.id} veritabanına yazıldı');
  }

  // ---- Sorumluluk 4: bildirim (bilgi işlem + pazarlama) ----
  void sendConfirmationEmail(Order order) {
    print('    [KÖTÜ] ${order.email} adresine e-posta gönderildi');
  }

  // ---- Sorumluluk 5: akış yönetimi ----
  void process(Order order) {
    final toplam = calculateTotal(order);
    saveToDatabase(order);
    sendConfirmationEmail(order);
    print(
      '    [KÖTÜ] ${order.id} işlendi, toplam $toplam, '
      '${loyaltyPoints(order)} puan',
    );
  }
}

// ############################################################################
//
//  BÖLÜM 4 — OCP: SWITCH YERİNE STRATEJİ
//
//  "Open for extension, closed for modification" cümlesinin somut hali:
//  yeni bir müşteri tipi eklemek YENİ BİR SINIF yazmak demek olsun,
//  mevcut sınıfları düzenlemek değil.
//
//  Üç switch'in üçü de tek bir sözleşmede toplanıyor. Çünkü üçü de
//  aynı soruyu farklı açılardan soruyordu: "bu müşteri tipi ne yapar?"
//
// ############################################################################

abstract interface class CustomerTier {
  String get badge;
  Money discountFor(Money subtotal);
  int loyaltyPointsFor(Money subtotal);
}

class IndividualTier implements CustomerTier {
  const IndividualTier();

  @override
  String get badge => 'Bireysel';

  @override
  Money discountFor(Money subtotal) => Money.zero;

  @override
  int loyaltyPointsFor(Money subtotal) => subtotal.kurus ~/ 10000;
}

class CorporateTier implements CustomerTier {
  const CorporateTier();

  @override
  String get badge => 'Kurumsal';

  @override
  Money discountFor(Money subtotal) => subtotal.percent(10);

  @override
  int loyaltyPointsFor(Money subtotal) => subtotal.kurus ~/ 5000;
}

class VipTier implements CustomerTier {
  const VipTier();

  @override
  String get badge => 'VIP';

  @override
  Money discountFor(Money subtotal) => subtotal.percent(20);

  @override
  int loyaltyPointsFor(Money subtotal) => subtotal.kurus ~/ 2000;
}

/// Kargo politikası — lojistik ekibinin alanı.
abstract interface class ShippingPolicy {
  String get name;
  Money costFor(Order order, Money subtotal);
}

class RegionalShipping implements ShippingPolicy {
  const RegionalShipping();

  @override
  String get name => 'Bölgesel tarife';

  @override
  Money costFor(Order order, Money subtotal) => switch (order.region) {
    Region.domestic => Money.lira(49.90),
    Region.neighboring => Money.lira(120),
    Region.overseas => Money.lira(340),
  };
}

/// SARMALAYICI: belirli tutarın üstünde kargoyu ücretsizleştirir.
/// Mevcut politikayı değiştirmeden davranış ekliyoruz (Gün 14 deseni).
class FreeOverThreshold implements ShippingPolicy {
  final ShippingPolicy _inner;
  final Money threshold;

  const FreeOverThreshold(this._inner, {required this.threshold});

  @override
  String get name => '${_inner.name} (+$threshold üstü ücretsiz)';

  @override
  Money costFor(Order order, Money subtotal) =>
      subtotal >= threshold ? Money.zero : _inner.costFor(order, subtotal);
}

/// Vergi politikası — muhasebenin alanı.
abstract interface class TaxPolicy {
  String get name;
  Money taxFor(Money amount);
}

class VatTax implements TaxPolicy {
  final int percent;
  const VatTax({this.percent = 20});

  @override
  String get name => 'KDV %$percent';

  @override
  Money taxFor(Money amount) => amount.percent(percent);
}

class TaxExempt implements TaxPolicy {
  const TaxExempt();

  @override
  String get name => 'Vergiden muaf';

  @override
  Money taxFor(Money amount) => Money.zero;
}

// ############################################################################
//
//  BÖLÜM 3 — SRP REFACTOR
//
//  Beş sorumluluk, beş sınıf. Her birinin TEK bir değişme sebebi var.
//
// ############################################################################

/// Hesaplama sonucunu taşıyan immutable nesne.
class OrderTotals {
  final Money subtotal;
  final Money discount;
  final Money taxable;
  final Money tax;
  final Money shipping;
  final Money total;
  final int loyaltyPoints;

  const OrderTotals({
    required this.subtotal,
    required this.discount,
    required this.taxable,
    required this.tax,
    required this.shipping,
    required this.total,
    required this.loyaltyPoints,
  });
}

/// SORUMLULUK: tutarları hesaplamak.
/// DEĞİŞME SEBEBİ: hesaplama ADIMLARININ sırası değişirse.
/// (Oranların kendisi değişirse burası değil, politikalar değişir.)
class OrderCalculator {
  final CustomerTier tier;
  final TaxPolicy taxPolicy;
  final ShippingPolicy shippingPolicy;

  const OrderCalculator({
    required this.tier,
    required this.taxPolicy,
    required this.shippingPolicy,
  });

  OrderTotals calculate(Order order) {
    final subtotal = order.subtotal;
    final discount = tier.discountFor(subtotal);
    final taxable = subtotal - discount;
    final tax = taxPolicy.taxFor(taxable);
    final shipping = shippingPolicy.costFor(order, taxable);

    return OrderTotals(
      subtotal: subtotal,
      discount: discount,
      taxable: taxable,
      tax: tax,
      shipping: shipping,
      total: taxable + tax + shipping,
      loyaltyPoints: tier.loyaltyPointsFor(subtotal),
    );
  }
}

/// SORUMLULUK: faturayı metne çevirmek.
/// DEĞİŞME SEBEBİ: hukuk yeni bir ibare isterse.
abstract interface class InvoiceRenderer {
  String get formatName;
  String render(Order order, OrderTotals totals, CustomerTier tier);
}

class TextInvoiceRenderer implements InvoiceRenderer {
  const TextInvoiceRenderer();

  @override
  String get formatName => 'metin';

  @override
  String render(Order order, OrderTotals t, CustomerTier tier) {
    final b = StringBuffer();
    b.writeln('FATURA ${order.id}');
    b.writeln('${order.customerName} — ${tier.badge}');
    for (final l in order.lines) {
      b.writeln('  ${l.product} x${l.quantity}  ${l.subtotal}');
    }
    b.writeln('  Ara toplam : ${t.subtotal}');
    b.writeln('  İndirim    : -${t.discount}');
    b.writeln('  Vergi      : ${t.tax}');
    b.writeln('  Kargo      : ${t.shipping}');
    b.write('  TOPLAM     : ${t.total}');
    return b.toString();
  }
}

class CsvInvoiceRenderer implements InvoiceRenderer {
  const CsvInvoiceRenderer();

  @override
  String get formatName => 'csv';

  @override
  String render(Order order, OrderTotals t, CustomerTier tier) =>
      'id,musteri,tip,ara,indirim,vergi,kargo,toplam\n'
      '${order.id},${order.customerName},${tier.badge},'
      '${t.subtotal.lira},${t.discount.lira},${t.tax.lira},'
      '${t.shipping.lira},${t.total.lira}';
}

/// SORUMLULUK: siparişi saklamak.
/// DEĞİŞME SEBEBİ: veritabanı teknolojisi değişirse.
abstract interface class OrderRepository {
  void save(Order order, OrderTotals totals);
}

class InMemoryOrderRepository implements OrderRepository {
  final List<String> saved = [];

  @override
  void save(Order order, OrderTotals totals) {
    saved.add('${order.id} -> ${totals.total}');
  }
}

/// SORUMLULUK: müşteriye haber vermek.
/// DEĞİŞME SEBEBİ: iletişim kanalı veya metni değişirse.
abstract interface class OrderNotifier {
  void confirm(Order order, OrderTotals totals);
}

class EmailOrderNotifier implements OrderNotifier {
  final List<String> sent = [];

  @override
  void confirm(Order order, OrderTotals totals) {
    sent.add('${order.email}: ${order.id} onaylandı (${totals.total})');
  }
}

/// SORUMLULUK: adımları sırayla çalıştırmak. Başka hiçbir şey.
/// DEĞİŞME SEBEBİ: iş akışının ADIMLARI değişirse.
///
/// Dikkat: bu sınıf hiçbir hesap yapmıyor, hiçbir metin üretmiyor.
/// Sadece koordine ediyor ve bütün parçaları dışarıdan alıyor (Gün 13).
class CheckoutCoordinator {
  final OrderCalculator calculator;
  final InvoiceRenderer renderer;
  final OrderRepository repository;
  final OrderNotifier notifier;
  final CustomerTier tier;

  const CheckoutCoordinator({
    required this.calculator,
    required this.renderer,
    required this.repository,
    required this.notifier,
    required this.tier,
  });

  String checkout(Order order) {
    final totals = calculator.calculate(order);
    repository.save(order, totals);
    notifier.confirm(order, totals);
    return renderer.render(order, totals, tier);
  }
}

// ############################################################################
//
//  BÖLÜM 5 — GENİŞLETME: SONRADAN EKLENEN TİPLER
//
//  Aşağıdaki üç sınıf, yukarıdaki hiçbir koda dokunulmadan eklendi.
//  OrderCalculator, CheckoutCoordinator, OrderTotals — hepsi bunları
//  tanımıyor ve yine de doğru çalışıyorlar.
//
// ############################################################################

/// YENİ MÜŞTERİ TİPİ — pazarlamanın yeni kampanyası.
class StudentTier implements CustomerTier {
  const StudentTier();

  @override
  String get badge => 'Öğrenci';

  @override
  Money discountFor(Money subtotal) => subtotal.percent(25);

  @override
  int loyaltyPointsFor(Money subtotal) => subtotal.kurus ~/ 8000;
}

/// YENİ FATURA FORMATI — hukuk HTML çıktı istedi.
class HtmlInvoiceRenderer implements InvoiceRenderer {
  const HtmlInvoiceRenderer();

  @override
  String get formatName => 'html';

  @override
  String render(Order order, OrderTotals t, CustomerTier tier) =>
      '<div class="invoice">\n'
      '  <h1>${order.id}</h1>\n'
      '  <p>${order.customerName} — ${tier.badge}</p>\n'
      '  <strong>${t.total}</strong>\n'
      '</div>';
}

/// YENİ KARGO KURALI — lojistik yurt dışına promosyon açtı.
class PromoShipping implements ShippingPolicy {
  final ShippingPolicy _inner;
  const PromoShipping(this._inner);

  @override
  String get name => '${_inner.name} (yurt dışı %50)';

  @override
  Money costFor(Order order, Money subtotal) {
    final normal = _inner.costFor(order, subtotal);
    return order.region == Region.overseas ? normal.percent(50) : normal;
  }
}

// ============================================================================
// ÇALIŞTIRMA
// ============================================================================

const _siparis = Order(
  id: 'ORD-2041',
  customerName: 'Ayşe Kaya',
  email: 'ayse@example.com',
  region: Region.domestic,
  customerType: CustomerType.student, // KÖTÜ tasarım bunu tanımıyor
  lines: [
    OrderLine(product: 'Mekanik Klavye', unitPrice: Money(245000), quantity: 1),
    OrderLine(product: 'Kablosuz Mouse', unitPrice: Money(62000), quantity: 2),
  ],
);

void main() {
  // ==========================================================================
  print('=== BÖLÜM 1: KÖTÜ TASARIM ÇALIŞIYOR ===');

  final kotu = OrderProcessor();
  print('    Müşteri tipi: ${_siparis.customerType.name}');
  print('    Rozet    : ${kotu.badgeLabel(_siparis)}');
  print('    İndirim  : ${kotu.calculateDiscount(_siparis)}');
  print('    Puan     : ${kotu.loyaltyPoints(_siparis)}');
  print('    Toplam   : ${kotu.calculateTotal(_siparis)}');
  print('');
  print('  Öğrenci tipi enum\'a eklendi ama üç switch\'in hiçbiri onu');
  print('  tanımıyor. Hepsi default dalına düştü:');
  print('    - indirim %25 olmalıydı, 0 geldi');
  print('    - puan hesaplanmalıydı, 0 geldi');
  print('    - rozet "Öğrenci" olmalıydı, "Bilinmiyor" geldi');
  print('');
  print('  KRİTİK NOKTA: hiçbir hata fırlamadı, hiçbir uyarı çıkmadı.');
  print('  Kod çalıştı ve YANLIŞ sonuç üretti. En kötü hata türü budur.');
  print('');
  print('  (Not: switch\'lerde default olmasaydı Dart derleme');
  print('  hatası verirdi — bu daha iyi. Ama yine de üç ayrı yeri');
  print('  bulup düzenlemen gerekirdi.)');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 2: SMELL CHECK ===');

  const kokular = [
    [
      'Adında Manager/Processor/Handler/Util geçiyor',
      'OrderProcessor — ne işlediği belirsiz',
    ],
    [
      'Sınıfı anlatırken "ve" demek zorunda kalıyorsun',
      'hesaplar VE biçimler VE kaydeder VE e-posta atar',
    ],
    [
      'Aynı enum üzerinde birden fazla switch var',
      'CustomerType üzerinde 3 switch',
    ],
    [
      'Alakasız bağımlılıklar bir arada',
      'veritabanı + e-posta + fiyat + metin',
    ],
    [
      'Farklı ekipler aynı dosyayı değiştiriyor',
      'muhasebe, pazarlama, lojistik, hukuk, BT',
    ],
    [
      'Test etmek için çok sayıda sahte nesne gerekiyor',
      'fiyat testi için DB ve SMTP de lazım',
    ],
  ];

  print('  ${'BELİRTİ'.padRight(48)}BU ÖRNEKTE');
  print('  ${'-' * 48}${'-' * 34}');
  for (final k in kokular) {
    print('  ${k[0].padRight(48)}${k[1]}');
  }

  print('');
  print('  En kullanışlı test: "Bu sınıfı değiştirmemi kim isteyebilir?"');
  print('  Cevapta birden fazla ekip varsa SRP ihlali var demektir.');
  print('  OrderProcessor için cevap: beş ayrı ekip.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 3: SRP REFACTOR SONRASI ===');

  const bolunmus = [
    ['OrderCalculator', 'tutarları hesaplar', 'hesap ADIMLARI değişirse'],
    ['CustomerTier', 'müşteri tipine özel kurallar', 'pazarlama'],
    ['TaxPolicy', 'vergi', 'muhasebe'],
    ['ShippingPolicy', 'kargo', 'lojistik'],
    ['InvoiceRenderer', 'fatura metni', 'hukuk'],
    ['OrderRepository', 'kayıt', 'bilgi işlem'],
    ['OrderNotifier', 'bildirim', 'bilgi işlem'],
    ['CheckoutCoordinator', 'adımları sıralar', 'iş akışı değişirse'],
  ];

  print('  ${'SINIF'.padRight(22)}${'SORUMLULUK'.padRight(30)}DEĞİŞME SEBEBİ');
  print('  ${'-' * 22}${'-' * 30}${'-' * 24}');
  for (final b in bolunmus) {
    print('  ${b[0].padRight(22)}${b[1].padRight(30)}${b[2]}');
  }
  print('');

  final repo = InMemoryOrderRepository();
  final notifier = EmailOrderNotifier();

  final coordinator = CheckoutCoordinator(
    tier: const StudentTier(),
    calculator: const OrderCalculator(
      tier: StudentTier(),
      taxPolicy: VatTax(),
      shippingPolicy: FreeOverThreshold(
        RegionalShipping(),
        threshold: Money(300000),
      ),
    ),
    renderer: const TextInvoiceRenderer(),
    repository: repo,
    notifier: notifier,
  );

  print(coordinator.checkout(_siparis));
  print('');
  print('  Kayıt   : ${repo.saved.first}');
  print('  Bildirim: ${notifier.sent.first}');
  print('  Öğrenci indirimi doğru uygulandı, puan doğru hesaplandı.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 5: BEFORE / AFTER — KAÇ YERİ DEĞİŞTİRDİK? ===');

  print('  DEĞİŞİKLİK 1: Yeni müşteri tipi (Öğrenci, %25 indirim)');
  print('    ÖNCE : 3 yer — calculateDiscount, loyaltyPoints, badgeLabel');
  print('           (+ unutulursa sessiz yanlış sonuç)');
  print('    SONRA: 1 yeni sınıf (StudentTier), 0 düzenleme');
  print('');

  print('  DEĞİŞİKLİK 2: Yeni fatura formatı (HTML)');
  print('    ÖNCE : 1 yer — formatInvoice içindeki switch');
  print('           (+ o metot zaten fiyat hesabı da çağırıyor,');
  print('            yani hukuk değişikliği muhasebe koduna dokunuyor)');
  print('    SONRA: 1 yeni sınıf (HtmlInvoiceRenderer), 0 düzenleme');
  print('');

  print('  DEĞİŞİKLİK 3: Yurt dışı kargoya %50 promosyon');
  print('    ÖNCE : 1 yer — calculateShipping switch\'i, iç içe if\'lerle');
  print('    SONRA: 1 yeni sarmalayıcı (PromoShipping), 0 düzenleme');
  print('');

  print('  Üçü birden, mevcut hiçbir sınıfa dokunulmadan:');

  final promoRepo = InMemoryOrderRepository();
  final promoNotifier = EmailOrderNotifier();
  const yurtdisi = Order(
    id: 'ORD-2042',
    customerName: 'Mert Aslan',
    email: 'mert@example.com',
    region: Region.overseas,
    lines: [
      OrderLine(product: 'Kulaklık', unitPrice: Money(120000), quantity: 1),
    ],
  );

  final yeniAkis = CheckoutCoordinator(
    tier: const StudentTier(),
    calculator: const OrderCalculator(
      tier: StudentTier(),
      taxPolicy: TaxExempt(),
      shippingPolicy: PromoShipping(RegionalShipping()),
    ),
    renderer: const HtmlInvoiceRenderer(),
    repository: promoRepo,
    notifier: promoNotifier,
  );

  print(yeniAkis.checkout(yurtdisi));
  print('');
  print('  TOPLAM: 3 yeni sınıf, 0 satır düzenleme.');
  print('  "Open for extension, closed for modification" bu demek.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 6: NÜANSLAR ===');

  print('  --- SRP "küçük sınıf" demek DEĞİLDİR ---');
  print('  Her metodu ayrı sınıfa koymak SRP değil, parçalama hastalığıdır.');
  print('  Ölçüt metot sayısı değil, DEĞİŞME SEBEBİ sayısı.');
  print('  OrderCalculator\'ın altı satırlık bir metodu var ama tek');
  print('  sorumluluğu var — bölmeye gerek yok.');
  print('');
  print('  Yanlış bölme işareti: iki sınıf her zaman birlikte değişiyorsa,');
  print('  onlar zaten tek bir sorumluluktu. Geri birleştir.');
  print('');

  print('  --- OCP her yere uygulanmaz ---');
  print('  Her şeye açık olamazsın. Hangi EKSENDE değişiklik bekliyorsan');
  print('  ona açık olursun; diğer eksenler kapalı kalır.');
  print('');
  print('  Bu tasarımda açık eksenler: müşteri tipi, vergi, kargo, format.');
  print('  Kapalı eksen: "sipariş satırı" kavramının kendisi. Yarın');
  print('  satırlara alt-satır eklemek istesek her yeri değiştirirdik.');
  print('  Bu bir eksiklik değil, bilinçli bir sınır.');
  print('');
  print('  Üçüncü kez aynı switch\'i düzenlediğinde soyutlama zamanı');
  print('  gelmiştir. Birinci seferde değil — o zaman erken soyutlama');
  print('  olur ve muhtemelen yanlış ekseni seçersin.');
  print('');

  print('  --- İki ilkenin ilişkisi ---');
  print('  SRP "neyi ayır" der, OCP "nasıl genişlet" der.');
  print('  SRP olmadan OCP kurulamaz: bir sınıfın beş sorumluluğu varsa,');
  print('  hangi eksende genişleteceğini bile tanımlayamazsın.');
  print('  Önce böl, sonra sözleşmeye bağla.');
}
