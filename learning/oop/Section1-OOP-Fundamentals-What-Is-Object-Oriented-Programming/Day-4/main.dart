// ============================================================================
// GÜN 4 — SORUMLULUK, TELL DON'T ASK, COMMAND/QUERY, İŞ BİRLİĞİ  (Dart)
//
// Çalıştırmak için:  dart run gun4_sorumluluk_ve_isbirligi.dart
// veya dartpad.dev'e yapıştır.
//
// BÖLÜM 1 -> God object: her işi yapan dev sınıf (kötü örnek)
// BÖLÜM 2 -> Aynı iş, sorumluluklara bölünmüş hâli
// BÖLÜM 3 -> Tell, Don't Ask
// BÖLÜM 4 -> Command vs Query
// BÖLÜM 5 -> İş birliği: Order, Customer'ı kullanıyor
// ============================================================================

// ============================================================================
// BÖLÜM 1 — GOD OBJECT (KÖTÜ ÖRNEK)
//
// Bu sınıf çalışıyor. Sorun "hata veriyor" değil, sorun şu:
//   - Müşteri bilgisini tutuyor
//   - Sepet satırlarını tutuyor
//   - Fiyat hesaplıyor
//   - İndirim kurallarını biliyor
//   - Fatura METNİNİ biçimlendiriyor
//   - E-posta gönderiyor
//   - Veritabanına kaydediyor
//
// Yani "bu sınıf ne yapar?" sorusuna tek cümleyle cevap veremiyorsun.
// Test etmek için e-posta ve veritabanı da lazım. KDV oranı değişse bu
// dosyaya dokunman gerekiyor; e-posta şablonu değişse de aynı dosyaya.
// Birbiriyle alakasız iki sebep, tek dosyayı değiştirmeye zorluyor.
// ============================================================================

class OrderManager {
  String customerName;
  String customerEmail;
  int customerLoyaltyYears;
  List<Map<String, dynamic>> items = [];
  bool isPaid = false;

  OrderManager({
    required this.customerName,
    required this.customerEmail,
    required this.customerLoyaltyYears,
  });

  void addItem(String name, double price, int qty) {
    items.add({'name': name, 'price': price, 'qty': qty});
  }

  double calculateSubtotal() {
    double sum = 0;
    for (final item in items) {
      sum += (item['price'] as double) * (item['qty'] as int);
    }
    return sum;
  }

  double calculateDiscount() {
    // İndirim kuralı burada; ama gereken veri (sadakat yılı) da burada.
    // İkisi tesadüfen aynı yerde. Müşteri kavramı büyüyünce bu dağılacak.
    final subtotal = calculateSubtotal();
    if (customerLoyaltyYears >= 5) return subtotal * 0.15;
    if (customerLoyaltyYears >= 2) return subtotal * 0.08;
    return 0;
  }

  String formatInvoice() {
    // Sunum işi. Fiyat hesabıyla hiç ilgisi yok ama aynı sınıfta.
    final buffer = StringBuffer();
    buffer.writeln('FATURA - $customerName');
    for (final item in items) {
      buffer.writeln('  ${item['name']} x${item['qty']}');
    }
    buffer.writeln('  Toplam: ${calculateSubtotal() - calculateDiscount()}');
    return buffer.toString();
  }

  void sendEmail() {
    // Dış dünya işi. Bu yüzden bu sınıfı test etmek zorlaşıyor.
    print('[GOD] $customerEmail adresine e-posta gönderildi');
  }

  void saveToDatabase() {
    print('[GOD] Veritabanına kaydedildi');
  }
}

// ============================================================================
// BÖLÜM 2 — SORUMLULUKLARA BÖLÜNMÜŞ HÂLİ
//
// Her sınıfın tek cümlelik bir görevi var. Test: sınıfı anlatırken
// "ve" demek zorunda kalıyorsan, muhtemelen bölünmesi gerekiyor.
// ============================================================================

enum OrderStatus { draft, placed, paid, cancelled }

/// SORUMLULUK: Bir müşteriyi temsil eder ve müşteriye özgü kuralları bilir.
class Customer {
  final String id;
  final String name;
  final String email;
  final int loyaltyYears;

  Customer({
    required this.id,
    required this.name,
    required this.email,
    required this.loyaltyYears,
  }) {
    if (loyaltyYears < 0) {
      throw ArgumentError.value(loyaltyYears, 'loyaltyYears', 'Negatif olamaz');
    }
    if (!email.contains('@')) {
      throw ArgumentError.value(email, 'email', 'Geçersiz e-posta');
    }
  }

  // ---- QUERY ----
  bool get isVip => loyaltyYears >= 5;

  /// TELL, DON'T ASK'in kalbi burası.
  /// Dışarıdaki kod "kaç yıllık müşteri?" diye SORMUYOR.
  /// "Bu tutara ne kadar indirim yaparsın?" diye SÖYLÜYOR.
  ///
  /// İndirim kuralı, kuralın ihtiyaç duyduğu verinin (loyaltyYears)
  /// yanında duruyor. Kural değişirse tek bir yer değişir.
  double discountRateFor(double amount) {
    if (loyaltyYears >= 5) return 0.15;
    if (loyaltyYears >= 2) return 0.08;
    return 0;
  }

  @override
  String toString() => '$name${isVip ? ' (VIP)' : ''}';
}

/// SORUMLULUK: Siparişteki tek bir satırı temsil eder.
class OrderLine {
  final String productName;
  final double unitPrice;
  final int quantity;

  OrderLine({
    required this.productName,
    required this.unitPrice,
    required this.quantity,
  }) {
    if (unitPrice < 0) {
      throw ArgumentError.value(unitPrice, 'unitPrice', 'Negatif olamaz');
    }
    if (quantity <= 0) {
      throw ArgumentError.value(quantity, 'quantity', 'Pozitif olmalı');
    }
  }

  /// QUERY — hesaplar, hiçbir şeyi değiştirmez.
  double get subtotal => unitPrice * quantity;
}

/// SORUMLULUK: Bir siparişin satırlarını, durumunu ve tutarını yönetir.
/// Fatura biçimlendirmez. E-posta göndermez. Veritabanına yazmaz.
class Order {
  final String id;
  final Customer customer; // <-- İŞ BİRLİĞİ: Order bir Customer'a sahip
  final List<OrderLine> _lines = [];
  OrderStatus _status = OrderStatus.draft;

  Order({required this.id, required this.customer});

  // ==========================================================================
  // BÖLÜM 4 — QUERY'LER
  // Bilgi döndürürler, durumu DEĞİŞTİRMEZLER.
  // Kaç kez çağırırsan çağır sonuç aynıdır ve yan etkisi yoktur.
  // İsimlendirme: isim veya sıfat gibi okunurlar (subtotal, total, isEmpty).
  // ==========================================================================

  OrderStatus get status => _status;

  List<OrderLine> get lines => List.unmodifiable(_lines);

  bool get isEmpty => _lines.isEmpty;

  int get itemCount => _lines.fold<int>(0, (sum, l) => sum + l.quantity);

  double get subtotal => _lines.fold<double>(0, (sum, l) => sum + l.subtotal);

  /// BÖLÜM 5 — İŞ BİRLİĞİ
  /// Order, indirim kuralını BİLMİYOR. Customer'a soruyor.
  /// Order'ın bildiği tek şey: "indirim, ara toplamın bir oranıdır".
  /// Kuralın kendisi Customer'ın işi.
  double get discount => subtotal * customer.discountRateFor(subtotal);

  double get total => subtotal - discount;

  bool get canBePaid => _status == OrderStatus.placed;

  // ==========================================================================
  // COMMAND'LER
  // Durumu DEĞİŞTİRİRLER, tipik olarak bir şey döndürmezler (void).
  // İsimlendirme: emir kipi fiil gibi okunurlar (addLine, place, markAsPaid).
  // Aynı komutu iki kez çağırmak farklı sonuç doğurabilir.
  // ==========================================================================

  void addLine(OrderLine line) {
    if (_status != OrderStatus.draft) {
      throw StateError('Sadece taslak siparişe satır eklenebilir');
    }
    _lines.add(line);
  }

  void place() {
    if (_status != OrderStatus.draft) {
      throw StateError('Sipariş zaten verilmiş (durum: $_status)');
    }
    if (isEmpty) {
      throw StateError('Boş sipariş verilemez');
    }
    _status = OrderStatus.placed;
  }

  void markAsPaid() {
    if (!canBePaid) {
      throw StateError('Bu sipariş ödenemez (durum: $_status)');
    }
    _status = OrderStatus.paid;
  }

  void cancel() {
    if (_status == OrderStatus.paid) {
      throw StateError('Ödenmiş sipariş iptal edilemez');
    }
    _status = OrderStatus.cancelled;
  }

  // ==========================================================================
  // KARIŞIK METOT — BUNU YAPMA
  //
  // Hem durumu değiştiriyor hem bilgi döndürüyor. İsminden hangisini
  // yaptığı anlaşılmıyor. Çağıran taraf "sadece kontrol edeyim" diye
  // çağırdığında farkında olmadan siparişi ödenmiş yapıyor.
  // ==========================================================================
  //
  // bool checkAndMarkPaid() {
  //   if (canBePaid) {
  //     _status = OrderStatus.paid;
  //     return true;
  //   }
  //   return false;
  // }
  //
  // Doğrusu ikiye ayırmak: önce canBePaid (query), sonra markAsPaid (command).
}

/// SORUMLULUK: Bir siparişi okunabilir metne çevirir. Başka hiçbir şey.
/// Order'ı hiç değiştirmez, sadece okur.
class InvoicePrinter {
  final String currencySymbol;

  const InvoicePrinter({this.currencySymbol = '₺'});

  String render(Order order) {
    String money(double v) => '${v.toStringAsFixed(2)} $currencySymbol';

    final buffer = StringBuffer();
    buffer.writeln('---------------------------------------');
    buffer.writeln('FATURA  #${order.id}');
    buffer.writeln('Müşteri: ${order.customer}');
    buffer.writeln('---------------------------------------');

    for (final line in order.lines) {
      final label = '${line.productName} x${line.quantity}';
      buffer.writeln('  ${label.padRight(28)}${money(line.subtotal)}');
    }

    buffer.writeln('---------------------------------------');
    buffer.writeln('  ${'Ara toplam'.padRight(28)}${money(order.subtotal)}');
    buffer.writeln('  ${'İndirim'.padRight(28)}-${money(order.discount)}');
    buffer.writeln('  ${'TOPLAM'.padRight(28)}${money(order.total)}');
    return buffer.toString();
  }
}

/// SORUMLULUK: Bildirim gönderir. Fiyat hesabından haberi yok.
class OrderNotifier {
  void notifyPaid(Order order) {
    print(
      '[BİLDİRİM] ${order.customer.email} -> '
      'Sipariş ${order.id} ödendi (${order.total.toStringAsFixed(2)} ₺)',
    );
  }
}

// ============================================================================
// BÖLÜM 3 — TELL, DON'T ASK: KÖTÜ VERSİYON
//
// Bu fonksiyon nesnenin alanlarını dışarı çekip kararı kendisi veriyor.
// Sorunlar:
//   - İndirim kuralı Customer'dan koptu, burada duruyor
//   - Aynı kural başka 5 dosyada da kopyalanacak
//   - Kural değişince hepsini bulman gerekecek, birini kaçıracaksın
//   - Customer, loyaltyYears'ı public tutmak zorunda kaldı
// ============================================================================

double hesaplaIndirimDisaridan(Order order) {
  final yil = order.customer.loyaltyYears; // ASK
  final ara = order.subtotal; // ASK
  if (yil >= 5) return ara * 0.15; // ve kararı biz veriyoruz
  if (yil >= 2) return ara * 0.08;
  return 0;
}

// ============================================================================
// ÇALIŞTIRMA
// ============================================================================

void main() {
  print('=== BÖLÜM 1: GOD OBJECT ===');

  final god = OrderManager(
    customerName: 'Ayşe Kaya',
    customerEmail: 'ayse@example.com',
    customerLoyaltyYears: 6,
  );
  god.addItem('Klavye', 850.0, 1);
  god.addItem('Mouse', 420.0, 2);
  print(god.formatInvoice());
  god.sendEmail();
  god.saveToDatabase();
  print('Bu sınıfı test etmek için e-posta ve veritabanı da lazım.');
  print('');

  print('=== BÖLÜM 2: SORUMLULUKLARA BÖLÜNMÜŞ ===');

  final musteri = Customer(
    id: 'C-1',
    name: 'Ayşe Kaya',
    email: 'ayse@example.com',
    loyaltyYears: 6,
  );

  final siparis = Order(id: 'ORD-1001', customer: musteri);
  siparis.addLine(
    OrderLine(productName: 'Klavye', unitPrice: 850, quantity: 1),
  );
  siparis.addLine(OrderLine(productName: 'Mouse', unitPrice: 420, quantity: 2));

  const yazici = InvoicePrinter();
  print(yazici.render(siparis));

  // Order'ı hiç değiştirmeden farklı bir biçimde yazdırabiliyoruz.
  const dolarYazici = InvoicePrinter(currencySymbol: '\$');
  print('Aynı sipariş, farklı yazıcı:');
  print(dolarYazici.render(siparis));

  print('=== BÖLÜM 3: TELL, DON\'T ASK ===');

  // ASK: alanları çek, kararı dışarıda ver
  print('Dışarıdan hesaplanan indirim : ${hesaplaIndirimDisaridan(siparis)}');

  // TELL: nesneye sor, kararı o versin
  print('Nesnenin kendi verdiği indirim: ${siparis.discount}');

  print('Sonuç aynı. Fark: ikincisinde kural TEK bir yerde yaşıyor.');
  print('Kuralı değiştirmek istersen sadece Customer.discountRateFor\'a');
  print('dokunursun; birinci yaklaşımda tüm projeyi taraman gerekir.');
  print('');

  print('=== BÖLÜM 4: COMMAND vs QUERY ===');

  print('--- Query\'ler (durumu değiştirmez) ---');
  print('  itemCount : ${siparis.itemCount}');
  print('  subtotal  : ${siparis.subtotal}');
  print('  total     : ${siparis.total}');
  print('  status    : ${siparis.status}');
  print('  Üç kez okudum, durum hâlâ: ${siparis.status}');

  print('--- Command\'ler (durumu değiştirir) ---');
  print('  önce  : ${siparis.status}');
  siparis.place();
  print('  place() sonrası : ${siparis.status}');
  siparis.markAsPaid();
  print('  markAsPaid() sonrası : ${siparis.status}');

  // Command'ler kuralları koruyor (Gün 3'teki invariant fikri):
  try {
    siparis.addLine(
      OrderLine(productName: 'Geç kalan ürün', unitPrice: 10, quantity: 1),
    );
  } on StateError catch (e) {
    print('  Ödenmiş siparişe ekleme reddedildi: ${e.message}');
  }

  try {
    siparis.cancel();
  } on StateError catch (e) {
    print('  Ödenmiş sipariş iptali reddedildi: ${e.message}');
  }
  print('');

  print('=== BÖLÜM 5: İŞ BİRLİĞİ ===');

  final bildirimci = OrderNotifier();
  bildirimci.notifyPaid(siparis);

  // Farklı müşteri -> aynı Order kodu, farklı indirim.
  // Order'a hiç dokunmadık; kural Customer'da olduğu için otomatik değişti.
  final yeniMusteri = Customer(
    id: 'C-2',
    name: 'Mert Aslan',
    email: 'mert@example.com',
    loyaltyYears: 0,
  );
  final ikinciSiparis = Order(id: 'ORD-1002', customer: yeniMusteri);
  ikinciSiparis.addLine(
    OrderLine(productName: 'Klavye', unitPrice: 850, quantity: 1),
  );
  ikinciSiparis.addLine(
    OrderLine(productName: 'Mouse', unitPrice: 420, quantity: 2),
  );

  print(
    'VIP müşteri  -> indirim: ${siparis.discount}, toplam: ${siparis.total}',
  );
  print(
    'Yeni müşteri -> indirim: ${ikinciSiparis.discount}, toplam: ${ikinciSiparis.total}',
  );
  print('Aynı ürünler, farklı sonuç. Order kodunda tek satır değişmedi.');
}
