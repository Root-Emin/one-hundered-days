// ============================================================================
// GÜN 8 — ABSTRACTION VE INTERFACE  (Dart)
//
// Çalıştırmak için:  dart run gun8_abstraction_ve_interface.dart
// veya dartpad.dev'e yapıştır.
//
// BÖLÜM 1 -> Alçak seviyeli çağıran kod (kötü örnek)
// BÖLÜM 2 -> Seviyeyi yükseltmek: checkout()
// BÖLÜM 3 -> Kararlı soyutlama: interface'ler
// BÖLÜM 4 -> Çağıranın sadeleşmesi
// BÖLÜM 5 -> Niyet bildiren isimlendirme
//
// NOT: 'abstract interface class' Dart 3 sözdizimidir. SDK'n eskiyse
// sadece 'abstract class' yazman yeterli; anlam aynı kalır.
// ============================================================================

// ============================================================================
// TEMEL VERİ NESNELERİ
// ============================================================================

class CartItem {
  final String sku;
  final String name;
  final int unitPriceKurus;
  final int quantity;

  const CartItem({
    required this.sku,
    required this.name,
    required this.unitPriceKurus,
    required this.quantity,
  });

  int get subtotalKurus => unitPriceKurus * quantity;
}

class Cart {
  final List<CartItem> _items = [];

  void add(CartItem item) => _items.add(item);

  List<CartItem> get items => List.unmodifiable(_items);
  bool get isEmpty => _items.isEmpty;
  int get itemCount => _items.fold<int>(0, (s, i) => s + i.quantity);
  int get subtotalKurus => _items.fold<int>(0, (s, i) => s + i.subtotalKurus);
}

class Customer {
  final String id;
  final String name;
  final String email;
  final String phone;

  const Customer({
    required this.id,
    required this.name,
    required this.email,
    required this.phone,
  });
}

class Order {
  static int _counter = 1000;

  final String id;
  final Customer customer;
  final List<CartItem> lines;
  final int totalKurus;
  final String paymentReference;
  final DateTime placedAt;

  Order({
    required this.customer,
    required this.lines,
    required this.totalKurus,
    required this.paymentReference,
  }) : id = 'ORD-${++_counter}',
       placedAt = DateTime.now();

  double get total => totalKurus / 100;

  @override
  String toString() => '$id | ${customer.name} | ${total.toStringAsFixed(2)} ₺';
}

class Inventory {
  final Map<String, int> _stock;

  Inventory(Map<String, int> initial) : _stock = Map.of(initial);

  int available(String sku) => _stock[sku] ?? 0;
  bool hasEnough(String sku, int qty) => available(sku) >= qty;

  void reserve(String sku, int qty) {
    if (!hasEnough(sku, qty)) {
      throw StateError('$sku için yeterli stok yok');
    }
    _stock[sku] = _stock[sku]! - qty;
  }
}

// ############################################################################
//
//  BÖLÜM 1 — ALÇAK SEVİYELİ ÇAĞIRAN KOD (KÖTÜ ÖRNEK)
//
//  Bu fonksiyon çalışıyor. Sorun şu: ne yaptığını anlamak için 40 satır
//  okumak gerekiyor ve okurken sürekli seviye değiştiriyorsun.
//
//    "Sepet boş mu?"           <- iş kuralı, yüksek seviye
//    "_stock[sku]! - qty"      <- veri yapısı detayı, alçak seviye
//    "kdv = ara * 20 / 100"    <- vergi kuralı, orta seviye
//    "buffer.writeln(...)"     <- metin biçimlendirme, en alçak seviye
//
//  Bir paragrafta hem stratejiden hem virgül kurallarından bahsetmek gibi.
//  Ayrıca bu fonksiyon KREDİ KARTINA ve E-POSTAYA çivilenmiş durumda:
//  ödeme yöntemini değiştirmek isteyen buraya girip kod değiştirmek zorunda.
//
// ############################################################################

void kotuCheckout(Cart cart, Customer customer, Inventory inventory) {
  // 1. Sepet kontrolü
  if (cart.items.isEmpty) {
    print('[KÖTÜ] Sepet boş, işlem iptal');
    return;
  }

  // 2. Stok kontrolü — veri yapısının içine giriyoruz
  for (final item in cart.items) {
    if (inventory.available(item.sku) < item.quantity) {
      print('[KÖTÜ] ${item.name} için stok yok, işlem iptal');
      return;
    }
  }

  // 3. Tutar hesabı — sihirli sayı burada
  int ara = 0;
  for (final item in cart.items) {
    ara += item.unitPriceKurus * item.quantity;
  }
  final kdv = (ara * 20) ~/ 100;
  var toplam = ara + kdv;

  // 4. Kargo kuralı — başka bir sihirli sayı
  if (toplam < 50000) {
    toplam += 4990;
  }

  // 5. Ödeme — kredi kartına çivilenmiş
  final kartNo = '4111111111111111';
  final maskeli = '**** **** **** ${kartNo.substring(kartNo.length - 4)}';
  if (toplam > 10000000) {
    print('[KÖTÜ] Kart limiti aşıldı');
    return;
  }
  final referans = 'CC-${DateTime.now().millisecondsSinceEpoch}';
  print(
    '[KÖTÜ] $maskeli karttan ${(toplam / 100).toStringAsFixed(2)} ₺ çekildi',
  );

  // 6. Stok düşme — yine veri yapısının içi
  for (final item in cart.items) {
    inventory.reserve(item.sku, item.quantity);
  }

  // 7. Sipariş kaydı
  final siparis = Order(
    customer: customer,
    lines: cart.items,
    totalKurus: toplam,
    paymentReference: referans,
  );

  // 8. Bildirim — e-postaya çivilenmiş, metin de burada üretiliyor
  final buffer = StringBuffer();
  buffer.writeln('Sayın ${customer.name},');
  buffer.writeln('${siparis.id} numaralı siparişiniz alındı.');
  buffer.writeln('Tutar: ${siparis.total.toStringAsFixed(2)} ₺');
  print('[KÖTÜ] ${customer.email} adresine e-posta gönderildi:');
  print(
    buffer
        .toString()
        .trimRight()
        .split('\n')
        .map((l) => '        $l')
        .join('\n'),
  );

  print('[KÖTÜ] Sipariş tamamlandı: ${siparis.id}');
}

// ############################################################################
//
//  BÖLÜM 3 — KARARLI SOYUTLAMALAR (INTERFACE'LER)
//
//  Bir interface, "ne yapılacağını" söyler, "nasıl yapılacağını" söylemez.
//  Bir SÖZLEŞMEDİR: bunu implement eden herkes bu operasyonları sunmak
//  zorundadır, ama nasıl sunduğu kendi bileceği iş.
//
//  DART'A ÖZGÜ NOT:
//  Dart'ta ayrı bir 'interface' anahtar kelimesi yoktur — her sınıf
//  kendiliğinden bir interface tanımlar. 'implements' dediğinde o sınıfın
//  KODUNU değil, sadece İMZALARINI devralırsın ve hepsini yazmak
//  zorundasın. 'extends' ise kodu da devralır.
//
//  Dart 3'te 'abstract interface class' yazarak "bu sadece sözleşme,
//  kimse bundan extends etmesin" diye niyetini açıkça belirtebilirsin.
//
// ############################################################################

/// Ödeme sonucunun taşıyıcısı. Immutable.
class PaymentResult {
  final bool isSuccessful;
  final String? reference;
  final String? errorMessage;

  const PaymentResult.success(this.reference)
    : isSuccessful = true,
      errorMessage = null;

  const PaymentResult.failure(this.errorMessage)
    : isSuccessful = false,
      reference = null;
}

/// ---------------------------------------------------------------------------
/// SÖZLEŞME 1: Ödeme yöntemi
///
/// Dikkat: burada "kart", "cüzdan", "havale" gibi hiçbir kelime yok.
/// Sözleşme sadece şunu söylüyor: bir tutar tahsil edilebilmeli ve
/// sonucu bildirilmeli. Bu yüzden KARARLI — yarın yeni bir ödeme
/// yöntemi çıkarsa bu satırlar değişmez.
/// ---------------------------------------------------------------------------
abstract interface class PaymentMethod {
  String get displayName;
  PaymentResult charge(int amountKurus);
}

class CreditCardPayment implements PaymentMethod {
  final String _cardNumber;
  final int _limitKurus;

  CreditCardPayment({required String cardNumber, int limitKurus = 10000000})
    : _cardNumber = cardNumber,
      _limitKurus = limitKurus;

  String get _masked => '**** ${_cardNumber.substring(_cardNumber.length - 4)}';

  @override
  String get displayName => 'Kredi Kartı ($_masked)';

  @override
  PaymentResult charge(int amountKurus) {
    if (amountKurus > _limitKurus) {
      return const PaymentResult.failure('Kart limiti yetersiz');
    }
    return PaymentResult.success('CC-${DateTime.now().microsecondsSinceEpoch}');
  }
}

class WalletPayment implements PaymentMethod {
  int _balanceKurus;

  WalletPayment({required int balanceKurus}) : _balanceKurus = balanceKurus;

  @override
  String get displayName => 'Dijital Cüzdan';

  @override
  PaymentResult charge(int amountKurus) {
    if (amountKurus > _balanceKurus) {
      final eksik = (amountKurus - _balanceKurus) / 100;
      return PaymentResult.failure(
        'Cüzdan bakiyesi yetersiz (${eksik.toStringAsFixed(2)} ₺ eksik)',
      );
    }
    _balanceKurus -= amountKurus;
    return PaymentResult.success(
      'WLT-${DateTime.now().microsecondsSinceEpoch}',
    );
  }
}

class CashOnDelivery implements PaymentMethod {
  static const int _maxKurus = 200000; // 2.000,00 ₺

  @override
  String get displayName => 'Kapıda Ödeme';

  @override
  PaymentResult charge(int amountKurus) {
    if (amountKurus > _maxKurus) {
      return const PaymentResult.failure('Kapıda ödeme üst sınırı 2.000,00 ₺');
    }
    // Burada para hemen tahsil edilmiyor; sadece taahhüt kaydediliyor.
    // Çağıran bunu bilmiyor ve bilmesine gerek de yok.
    return PaymentResult.success(
      'COD-${DateTime.now().microsecondsSinceEpoch}',
    );
  }
}

/// ---------------------------------------------------------------------------
/// SÖZLEŞME 2: Bildirim kanalı
/// ---------------------------------------------------------------------------
abstract interface class Notifier {
  void notifyOrderPlaced(Order order);
}

class EmailNotifier implements Notifier {
  @override
  void notifyOrderPlaced(Order order) {
    print(
      '    [E-POSTA] ${order.customer.email} -> '
      '${order.id} siparişiniz alındı (${order.total.toStringAsFixed(2)} ₺)',
    );
  }
}

class SmsNotifier implements Notifier {
  @override
  void notifyOrderPlaced(Order order) {
    print('    [SMS] ${order.customer.phone} -> ${order.id} onaylandı');
  }
}

/// Soyutlamanın güzel tarafı: bu sınıf hem Notifier'ı IMPLEMENT ediyor
/// hem de içinde Notifier'lar TUTUYOR. Çağıran taraf tek bir bildirimci
/// mi yoksa beş tanesi mi olduğunu ayırt edemez.
class MultiNotifier implements Notifier {
  final List<Notifier> _channels;

  MultiNotifier(this._channels);

  @override
  void notifyOrderPlaced(Order order) {
    for (final channel in _channels) {
      channel.notifyOrderPlaced(order);
    }
  }
}

// ############################################################################
//
//  BÖLÜM 2 — SEVİYEYİ YÜKSELTMEK
//
//  Aşağıdaki checkout() metodu, kotuCheckout'un yaptığı işin AYNISINI
//  yapıyor. Fark: gövdesi altı satır ve her satır aynı soyutlama
//  seviyesinde. Metot bir İÇİNDEKİLER TABLOSU gibi okunuyor.
//
//  Detaylar kaybolmadı — private metotlara indi. Merak edersen açıp
//  bakarsın; merak etmezsen görmen gerekmiyor.
//
// ############################################################################

sealed class CheckoutResult {
  const CheckoutResult();
}

class CheckoutSuccess extends CheckoutResult {
  final Order order;
  const CheckoutSuccess(this.order);
}

class CheckoutFailure extends CheckoutResult {
  final String reason;
  const CheckoutFailure(this.reason);
}

class CheckoutService {
  static const int vatPercent = 20;
  static const int shippingFeeKurus = 4990;
  static const int freeShippingThresholdKurus = 50000;

  final Inventory _inventory;
  final PaymentMethod _paymentMethod; // <-- somut sınıf değil, SÖZLEŞME
  final Notifier _notifier; // <-- somut sınıf değil, SÖZLEŞME

  CheckoutService({
    required Inventory inventory,
    required PaymentMethod paymentMethod,
    required Notifier notifier,
  }) : _inventory = inventory,
       _paymentMethod = paymentMethod,
       _notifier = notifier;

  // ==========================================================================
  // PUBLIC API — TEK BİR NİYET
  //
  // Çağıran taraf sadece bunu bilir: "satın alma işlemini tamamla".
  // KDV'den, kargo eşiğinden, stok rezervasyonundan haberi yok.
  // ==========================================================================
  CheckoutResult checkout(Cart cart, Customer customer) {
    final hazirlik = _ensureCartCanBeOrdered(cart);
    if (hazirlik != null) return CheckoutFailure(hazirlik);

    final total = _calculateOrderTotal(cart);

    final payment = _paymentMethod.charge(total);
    if (!payment.isSuccessful) {
      return CheckoutFailure(payment.errorMessage ?? 'Ödeme alınamadı');
    }

    final order = _placeOrder(cart, customer, total, payment.reference!);
    _confirmToCustomer(order);

    return CheckoutSuccess(order);
  }

  // ==========================================================================
  // ALT SEVİYE ADIMLAR
  //
  // Her biri tek bir işi yapıyor ve adı ne yaptığını söylüyor.
  // checkout() bunları okurken sen de okuyabiliyorsun.
  // ==========================================================================

  /// Sipariş verilebilir durumda mı? Sorun varsa sebebini döndürür,
  /// yoksa null. (Gün 7'deki fail-fast: değiştirmeden önce kontrol.)
  String? _ensureCartCanBeOrdered(Cart cart) {
    if (cart.isEmpty) return 'Sepet boş';

    for (final item in cart.items) {
      if (!_inventory.hasEnough(item.sku, item.quantity)) {
        return '${item.name} için yeterli stok yok '
            '(${_inventory.available(item.sku)} adet kaldı)';
      }
    }
    return null;
  }

  int _calculateOrderTotal(Cart cart) {
    final subtotal = cart.subtotalKurus;
    final withVat = subtotal + _vatFor(subtotal);
    return withVat + _shippingFeeFor(withVat);
  }

  int _vatFor(int amountKurus) => (amountKurus * vatPercent) ~/ 100;

  int _shippingFeeFor(int amountKurus) =>
      amountKurus >= freeShippingThresholdKurus ? 0 : shippingFeeKurus;

  Order _placeOrder(Cart cart, Customer customer, int total, String reference) {
    for (final item in cart.items) {
      _inventory.reserve(item.sku, item.quantity);
    }
    return Order(
      customer: customer,
      lines: cart.items,
      totalKurus: total,
      paymentReference: reference,
    );
  }

  void _confirmToCustomer(Order order) => _notifier.notifyOrderPlaced(order);
}

// ############################################################################
//
//  BÖLÜM 4 — ÇAĞIRANIN SADELEŞMESİ
//
//  Bu fonksiyon "dış dünya"yı temsil ediyor (bir buton handler'ı gibi).
//  Ne KDV oranını biliyor, ne ödeme yöntemini, ne bildirim kanalını.
//  Sadece niyeti söylüyor ve sonucu ele alıyor.
//
// ############################################################################

void satinAlmaButonu(CheckoutService service, Cart cart, Customer customer) {
  final result = service.checkout(cart, customer);

  switch (result) {
    case CheckoutSuccess(:final order):
      print('    ✓ Sipariş oluşturuldu: $order');
    case CheckoutFailure(:final reason):
      print('    ✗ Sipariş oluşturulamadı: $reason');
  }
}

// ============================================================================
// ÇALIŞTIRMA
// ============================================================================

Cart _sepetUret() {
  return Cart()
    ..add(
      const CartItem(
        sku: 'KLV-01',
        name: 'Mekanik Klavye',
        unitPriceKurus: 245000,
        quantity: 1,
      ),
    )
    ..add(
      const CartItem(
        sku: 'MSE-02',
        name: 'Kablosuz Mouse',
        unitPriceKurus: 62000,
        quantity: 2,
      ),
    );
}

const musteri = Customer(
  id: 'C-1',
  name: 'Ayşe Kaya',
  email: 'ayse@example.com',
  phone: '+90 555 000 00 00',
);

void main() {
  print('=== BÖLÜM 1: ALÇAK SEVİYELİ VERSİYON ===');
  kotuCheckout(_sepetUret(), musteri, Inventory({'KLV-01': 10, 'MSE-02': 10}));
  print('');
  print('Çalışıyor. Ama: 8 numaralı adım nerede biter, 3\'ün sihirli');
  print('sayıları ne demek, ödeme yöntemini değiştirmek için nereye');
  print('dokunmak gerekir? Hepsi tek gövdede iç içe.');
  print('');

  print('=== BÖLÜM 2 + 4: YÜKSEK SEVİYELİ VERSİYON ===');

  final envanter = Inventory({'KLV-01': 10, 'MSE-02': 10});

  final kartlaOdeme = CheckoutService(
    inventory: envanter,
    paymentMethod: CreditCardPayment(cardNumber: '4111111111111111'),
    notifier: EmailNotifier(),
  );

  print(
    '  Ödeme: ${CreditCardPayment(cardNumber: "4111111111111111").displayName}',
  );
  satinAlmaButonu(kartlaOdeme, _sepetUret(), musteri);
  print('  Kalan stok — klavye: ${envanter.available('KLV-01')}');
  print('');

  print('=== BÖLÜM 3: AYNI ÇAĞIRAN, FARKLI IMPLEMENTASYONLAR ===');
  print('Aşağıdaki dört senaryoda satinAlmaButonu() ve CheckoutService');
  print('kodunda TEK SATIR değişmiyor. Sadece constructor\'a farklı');
  print('nesneler veriyoruz.');
  print('');

  // --- Cüzdan + SMS ---
  print('  1) Cüzdan ile ödeme, SMS bildirimi:');
  satinAlmaButonu(
    CheckoutService(
      inventory: envanter,
      paymentMethod: WalletPayment(balanceKurus: 500000),
      notifier: SmsNotifier(),
    ),
    _sepetUret(),
    musteri,
  );

  // --- Cüzdan yetersiz ---
  print('  2) Cüzdanda yeterli para yok:');
  satinAlmaButonu(
    CheckoutService(
      inventory: envanter,
      paymentMethod: WalletPayment(balanceKurus: 1000),
      notifier: SmsNotifier(),
    ),
    _sepetUret(),
    musteri,
  );

  // --- Kapıda ödeme limiti ---
  print('  3) Kapıda ödeme (2.000 ₺ üst sınırı var):');
  satinAlmaButonu(
    CheckoutService(
      inventory: envanter,
      paymentMethod: CashOnDelivery(),
      notifier: EmailNotifier(),
    ),
    _sepetUret(),
    musteri,
  );

  // --- Çoklu bildirim ---
  print('  4) Kart + hem e-posta hem SMS:');
  satinAlmaButonu(
    CheckoutService(
      inventory: envanter,
      paymentMethod: CreditCardPayment(cardNumber: '5555444433332222'),
      notifier: MultiNotifier([EmailNotifier(), SmsNotifier()]),
    ),
    _sepetUret(),
    musteri,
  );
  print('  (MultiNotifier de sadece bir Notifier. Çağıran farkı bilmiyor.)');
  print('');

  print('=== YENİ ÖDEME YÖNTEMİ EKLEMEK ===');
  print('KriptoOdeme sınıfı dosyanın en altında tanımlı ve');
  print('CheckoutService onu hiç tanımıyor. Yine de çalışıyor:');
  satinAlmaButonu(
    CheckoutService(
      inventory: envanter,
      paymentMethod: KriptoOdeme(),
      notifier: EmailNotifier(),
    ),
    _sepetUret(),
    musteri,
  );
  print('');

  print('=== BÖLÜM 5: NİYET BİLDİREN İSİMLER ===');

  const isimler = [
    ['setStatusToPaidAndDecrementStock()', 'checkout()'],
    ['insertRowIntoOrdersTable()', 'placeOrder()'],
    ['loopItemsAndSumPrices()', 'calculateOrderTotal()'],
    ['sendSmtpMessage()', 'confirmToCustomer()'],
    ['getListWhereActiveIsTrue()', 'activeMembers'],
    ['processData()', 'normalizePhoneNumbers()'],
    ['handleClick()', 'submitApplication()'],
    ['doStuff()', '(hiçbir zaman kabul edilebilir değil)'],
  ];

  print('  ${'MAKİNEYİ ANLATAN'.padRight(38)}NİYETİ ANLATAN');
  print('  ${'-' * 38}${'-' * 30}');
  for (final cift in isimler) {
    print('  ${cift[0].padRight(38)}${cift[1]}');
  }

  print('');
  print('  İki test:');
  print('  1. Metodun içi tamamen değişse, adı yine doğru kalır mı?');
  print('     "sendSmtpMessage" -> SMTP\'den vazgeçince ad YALAN olur.');
  print('     "confirmToCustomer" -> kanal ne olursa olsun doğru kalır.');
  print('  2. "processData()" gibi bir ad, o metodun ne yaptığını');
  print('     senin de bilmediğinin işaretidir. Önce sorumluluğu netleştir.');
  print('');
  print('  Fazladan bir ipucu: "ve" içeren bir ad ("saveAndNotify")');
  print('  sınıfın değil metodun bölünmesi gerektiğini söyler.');
}

// ============================================================================
// SONRADAN EKLENEN ÖDEME YÖNTEMİ
//
// Bu sınıf CheckoutService'ten SONRA yazıldı ve CheckoutService onu
// tanımıyor. Yine de çalışıyor, çünkü ikisi de aynı sözleşmeye bakıyor.
//
// COUPLING (bağımlılık) burada tersine döndü:
//   Önce: CheckoutService -> CreditCardPayment  (somuta bağımlı)
//   Şimdi: CheckoutService -> PaymentMethod <- KriptoOdeme
//          (ikisi de soyuta bağımlı)
//
// Bu, ilerideki günlerde "Dependency Inversion" adıyla karşına çıkacak.
// ============================================================================

class KriptoOdeme implements PaymentMethod {
  @override
  String get displayName => 'Kripto Cüzdan';

  @override
  PaymentResult charge(int amountKurus) {
    return PaymentResult.success(
      'BTC-${DateTime.now().microsecondsSinceEpoch}',
    );
  }
}
