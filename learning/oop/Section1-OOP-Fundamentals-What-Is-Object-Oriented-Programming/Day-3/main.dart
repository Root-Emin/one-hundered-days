// ============================================================================
// GÜN 3 — CONSTRUCTOR, VALIDATION, DEFAULT, FACTORY  (Dart)
//
// Çalıştırmak için:  dart run gun3_constructor_ve_validation.dart
// veya dartpad.dev'e yapıştır.
//
// BÖLÜM 1 -> Constructor: zorunlu alanlar + default değerler
// BÖLÜM 2 -> Validation: geçersiz nesnenin DOĞMASINI engellemek
// BÖLÜM 3 -> Invariant: kuralın ömür boyu korunması
// BÖLÜM 4 -> Named constructor & factory constructor
// BÖLÜM 5 -> Factory'nin asıl gücü: var olan nesneyi geri döndürmek
// ============================================================================

// ============================================================================
// BÖLÜM 1-3 — ÜRÜN SINIFI
// ============================================================================

class Product {
  // ---- Değişmeyen alanlar ----
  final String name;
  final double price;
  final String currency;
  final String? description;

  // ---- Değişen durum ----
  int _stock;

  // ==========================================================================
  // ANA CONSTRUCTOR
  //
  // 'required' -> bu alanlar olmadan nesne üretilemez. Yarım bir Product
  //               diye bir şey olamaz; derleyici buna izin vermez.
  // '= TRY'    -> DEFAULT VALUE. Çağıran belirtmezse bu kullanılır.
  //               Böylece basit kullanım kısa, özel kullanım mümkün kalır.
  // ==========================================================================
  Product({
    required this.name,
    required this.price,
    this.currency = 'TRY', // default value
    int stock = 0, // default value
    this.description, // opsiyonel, varsayılanı null
  }) : _stock = stock {
    // <-- INITIALIZER LIST: gövdeden ÖNCE çalışır
    // ------------------------------------------------------------------
    // BÖLÜM 2 — VALIDATION
    //
    // Buradan bir exception fırlarsa constructor tamamlanmaz ve çağıran
    // taraf ASLA bu nesneyi eline geçiremez. Yani "geçersiz Product"
    // diye bir şey sistemde dolaşamaz.
    //
    // NOT: assert() kullanma. assert sadece debug'da çalışır, release
    // build'de tamamen silinir. Dışarıdan gelen veriyi (API, form, DB)
    // assert ile doğrularsan, canlıda hiçbir koruman kalmaz.
    // ------------------------------------------------------------------
    if (name.trim().isEmpty) {
      throw ArgumentError.value(name, 'name', 'Ürün adı boş olamaz');
    }
    if (price < 0) {
      throw ArgumentError.value(price, 'price', 'Fiyat negatif olamaz');
    }
    if (price.isNaN || price.isInfinite) {
      throw ArgumentError.value(
        price,
        'price',
        'Fiyat geçerli bir sayı olmalı',
      );
    }
    if (_stock < 0) {
      throw ArgumentError.value(stock, 'stock', 'Stok negatif olamaz');
    }
    if (currency.length != 3) {
      throw ArgumentError.value(
        currency,
        'currency',
        'Para birimi 3 harf olmalı',
      );
    }
  }

  // ==========================================================================
  // BÖLÜM 4 — NAMED CONSTRUCTOR
  //
  // Aynı sınıfı üretmenin farklı ama sık kullanılan yolları.
  // Product(name: 'x', price: 0) yerine Product.free(name: 'x') demek
  // NİYETİ okunur kılıyor: bu ürün bedava, sıfır fiyat bir hata değil.
  // ==========================================================================

  /// Redirecting constructor: işi ana constructor'a devrediyor.
  /// Bu sayede validation kuralları burada TEKRAR YAZILMIYOR.
  Product.free({required String name}) : this(name: name, price: 0, stock: 0);

  /// Stoksuz ürün — mağazada var ama şu an tükenmiş.
  Product.outOfStock({required String name, required double price})
    : this(name: name, price: price, stock: 0);

  // ==========================================================================
  // FACTORY CONSTRUCTOR
  //
  // Normal constructor'dan farkı: gövde çalıştırabilir ve NE DÖNDÜRECEĞİNE
  // kendisi karar verir. Burada JSON'u önce temizleyip doğruluyoruz,
  // sonra ana constructor'ı çağırıyoruz.
  // ==========================================================================
  factory Product.fromJson(Map<String, dynamic> json) {
    final rawName = json['name'];
    if (rawName is! String) {
      throw FormatException('name alanı String olmalı, gelen: $rawName');
    }

    final rawPrice = json['price'];
    if (rawPrice is! num) {
      throw FormatException('price alanı sayı olmalı, gelen: $rawPrice');
    }

    // '??' -> soldaki null ise sağdakini kullan. Default değer uygulamanın
    // bir başka yolu; JSON'da alan hiç olmayabilir.
    return Product(
      name: rawName,
      price: rawPrice.toDouble(),
      currency: json['currency'] as String? ?? 'TRY',
      stock: json['stock'] as int? ?? 0,
      description: json['description'] as String?,
    );
  }

  // ---- Getter'lar ----
  int get stock => _stock;
  bool get isAvailable => _stock > 0;
  bool get isFree => price == 0;

  String get formattedPrice => '${price.toStringAsFixed(2)} $currency';

  // ==========================================================================
  // BÖLÜM 3 — INVARIANT'IN ÖMÜR BOYU KORUNMASI
  //
  // Constructor'da doğrulama yapmak yetmez. "Stok asla negatif olamaz"
  // kuralı nesnenin DOĞUM ANINDA değil, HAYATI BOYUNCA geçerli olmalı.
  // Bu yüzden durumu değiştiren her metot aynı kuralı tekrar kontrol eder.
  // ==========================================================================

  void sell(int quantity) {
    if (quantity <= 0) {
      throw ArgumentError.value(quantity, 'quantity', 'Adet pozitif olmalı');
    }
    if (quantity > _stock) {
      throw StateError('Yetersiz stok: $_stock adet var, $quantity isteniyor');
    }
    _stock -= quantity;
    print('$name: $quantity adet satıldı, kalan stok $_stock');
  }

  void restock(int quantity) {
    if (quantity <= 0) {
      throw ArgumentError.value(quantity, 'quantity', 'Adet pozitif olmalı');
    }
    _stock += quantity;
    print('$name: $quantity adet eklendi, yeni stok $_stock');
  }

  /// Flutter'da çok kullanacaksın: mevcut nesneden, bazı alanları
  /// değiştirilmiş YENİ bir nesne üretir. Orijinal nesneye dokunmaz.
  Product copyWith({String? name, double? price, String? currency}) {
    return Product(
      name: name ?? this.name,
      price: price ?? this.price,
      currency: currency ?? this.currency,
      stock: _stock,
      description: description,
    );
  }

  @override
  String toString() {
    final stockText = isAvailable ? '$_stock adet' : 'TÜKENDİ';
    return '$name — $formattedPrice | $stockText';
  }
}

// ============================================================================
// BÖLÜM 5 — FACTORY'NİN ASIL GÜCÜ
//
// Normal constructor HER ZAMAN yeni bir nesne üretmek zorundadır.
// Factory constructor ise var olan bir nesneyi geri döndürebilir.
// Aşağıda 'TRY' için kaç kez Currency('TRY') dersen de bellekte tek
// nesne olur. (Gün 2'deki identity konusuyla doğrudan bağlantılı.)
// ============================================================================

class Currency {
  final String code;
  final String symbol;

  // Üretilmiş nesnelerin havuzu.
  static final Map<String, Currency> _cache = {};

  // Private constructor: dışarıdan Currency._(...) çağrılamaz.
  // Tek giriş kapısı aşağıdaki factory.
  Currency._(this.code, this.symbol);

  factory Currency(String code) {
    final normalized = code.toUpperCase();

    if (normalized.length != 3) {
      throw ArgumentError.value(code, 'code', 'Para birimi 3 harf olmalı');
    }

    // Varsa mevcut olanı döndür, yoksa üret ve sakla.
    return _cache.putIfAbsent(normalized, () {
      const symbols = {'TRY': '₺', 'USD': '\$', 'EUR': '€'};
      return Currency._(normalized, symbols[normalized] ?? normalized);
    });
  }

  @override
  String toString() => '$code ($symbol)';
}

// ============================================================================
// ÇALIŞTIRMA
// ============================================================================

void main() {
  print('=== BÖLÜM 1: CONSTRUCTOR VE DEFAULT DEĞERLER ===');

  // Sadece zorunlu alanları veriyoruz; gerisi default.
  final kalem = Product(name: 'Kurşun Kalem', price: 12.5);
  print(kalem);
  print('  para birimi (default): ${kalem.currency}');
  print('  stok (default): ${kalem.stock}');
  print('  açıklama (default): ${kalem.description}');

  // Default'ları ezip kendi değerlerimizi veriyoruz.
  final defter = Product(
    name: 'Spiralli Defter',
    price: 45,
    currency: 'USD',
    stock: 30,
    description: 'A4, 120 yaprak',
  );
  print(defter);
  print('');

  print('=== BÖLÜM 2: VALIDATION ===');

  // Aşağıdaki her deneme başarısız olacak; hiçbiri nesne üretemeyecek.
  final gecersizDenemeler = <String, void Function()>{
    'Boş isim': () => Product(name: '   ', price: 10),
    'Negatif fiyat': () => Product(name: 'Silgi', price: -5),
    'Negatif stok': () => Product(name: 'Silgi', price: 5, stock: -1),
    'Hatalı para birimi': () =>
        Product(name: 'Silgi', price: 5, currency: 'TRYY'),
  };

  gecersizDenemeler.forEach((baslik, deneme) {
    try {
      deneme();
      print('$baslik -> BEKLENMEDİK: nesne üretildi!');
    } on ArgumentError catch (e) {
      print('$baslik -> reddedildi: ${e.message}');
    }
  });
  print('');

  print('=== BÖLÜM 3: INVARIANT ÖMÜR BOYU ===');

  final maskeler = Product(name: 'Cerrahi Maske', price: 2, stock: 10);
  maskeler.sell(4);

  try {
    maskeler.sell(100); // stoktan fazla
  } on StateError catch (e) {
    print('Reddedildi: ${e.message}');
  }

  maskeler.restock(20);
  print('Son durum: $maskeler');
  print('Stok hâlâ negatif değil mi? -> ${maskeler.stock >= 0}');
  print('');

  print('=== BÖLÜM 4: NAMED VE FACTORY CONSTRUCTOR ===');

  // Named constructor: niyet okunur.
  final deneme = Product.free(name: 'Ücretsiz Deneme Sürümü');
  print('$deneme  (bedava mı: ${deneme.isFree})');

  final tukenmis = Product.outOfStock(name: 'Sınırlı Baskı Kitap', price: 250);
  print('$tukenmis  (satışta mı: ${tukenmis.isAvailable})');

  // Factory constructor: JSON'dan üretim.
  final apiCevabi = {
    'name': 'Termos',
    'price': 189.9,
    'stock': 7,
    // 'currency' ve 'description' yok -> default'lar devreye girecek
  };
  final termos = Product.fromJson(apiCevabi);
  print('JSON\'dan: $termos (currency default: ${termos.currency})');

  // Bozuk JSON reddediliyor.
  try {
    Product.fromJson({'name': 'Bozuk', 'price': 'yüz lira'});
  } on FormatException catch (e) {
    print('Bozuk JSON -> reddedildi: ${e.message}');
  }

  // copyWith: orijinali bozmadan varyant üretmek.
  final zamliTermos = termos.copyWith(price: 219.9);
  print(
    'Orijinal: ${termos.formattedPrice} | Zamlı: ${zamliTermos.formattedPrice}',
  );
  print('');

  print('=== BÖLÜM 5: FACTORY VAR OLAN NESNEYİ DÖNDÜRÜR ===');

  final try1 = Currency('TRY');
  final try2 = Currency('try'); // küçük harf, normalize edilecek
  final usd = Currency('USD');

  print('try1: $try1, try2: $try2, usd: $usd');
  print('identical(try1, try2) -> ${identical(try1, try2)}'); // true
  print('identical(try1, usd)  -> ${identical(try1, usd)}'); // false

  // Normal constructor bunu YAPAMAZ; her çağrıda yeni nesne üretmek
  // zorundadır. Product'ta her Product(...) yeni bir nesnedir:
  final p1 = Product(name: 'Aynı', price: 1);
  final p2 = Product(name: 'Aynı', price: 1);
  print('identical(p1, p2) -> ${identical(p1, p2)}'); // false
}
