// ============================================================================
// GÜN 9 — IMMUTABLE vs MUTABLE TASARIM  (Dart)
//
// Çalıştırmak için:  dart run gun9_immutable_vs_mutable.dart
// veya dartpad.dev'e yapıştır.
//
// BÖLÜM 1 -> Mutable yol: nesne kendini günceller
// BÖLÜM 2 -> Immutable yol: her değişiklik yeni nesne üretir
// BÖLÜM 3 -> Karşılaştırma: paylaşım, hata ayıklama, eşitlik, async
// BÖLÜM 4 -> Value Object vs Entity
// BÖLÜM 5 -> Hangisini ne zaman seçmeli
// ============================================================================

// ############################################################################
//
//  VALUE OBJECT: Money
//
//  Bir "değer nesnesi"nin üç işareti:
//    1. Kimliği yok — 10 ₺ ile 10 ₺ arasında fark yoktur
//    2. Değerleriyle tanımlanır — bu yüzden == değere göre çalışır
//    3. Immutable — 10 ₺ hiçbir zaman 20 ₺ "olmaz"; başka bir 20 ₺ vardır
//
//  Bunlar birbirine bağlı: kimliği olmayan bir şeyin değişmesi zaten
//  anlamsızdır. Sayılar da böyledir — 5 sayısını "değiştiremezsin".
//
// ############################################################################

class Money {
  final int kurus;

  const Money(this.kurus);
  const Money.zero() : kurus = 0;

  factory Money.lira(double amount) {
    if (amount.isNaN || amount.isInfinite) {
      throw ArgumentError.value(amount, 'amount', 'Geçerli bir sayı olmalı');
    }
    return Money((amount * 100).round());
  }

  double get lira => kurus / 100;

  // Operatörler yeni nesne döndürüyor; hiçbiri 'this'i değiştirmiyor.
  Money operator +(Money other) => Money(kurus + other.kurus);
  Money operator -(Money other) => Money(kurus - other.kurus);
  Money operator *(int factor) => Money(kurus * factor);
  bool operator >(Money other) => kurus > other.kurus;
  bool operator <(Money other) => kurus < other.kurus;

  Money percent(int p) => Money((kurus * p) ~/ 100);

  @override
  bool operator ==(Object other) =>
      identical(this, other) || (other is Money && other.kurus == kurus);

  @override
  int get hashCode => kurus.hashCode;

  @override
  String toString() => '${lira.toStringAsFixed(2)} ₺';
}

// ############################################################################
//
//  VALUE OBJECT: DateRange
//
//  Money'den bağımsız ama aynı kalıp: kimliksiz, değere göre eşit,
//  değişmez. Kural (başlangıç < bitiş) constructor'da korunuyor ve
//  nesne değişemediği için bir daha ASLA bozulamaz.
//
//  Immutable tasarımın en büyük sessiz kazancı bu: Gün 7'deki
//  "invariantı her mutator'da koru" derdi tamamen ortadan kalkıyor,
//  çünkü mutator yok.
//
// ############################################################################

class DateRange {
  final DateTime start;
  final DateTime end;

  DateRange({required this.start, required this.end}) {
    if (!start.isBefore(end)) {
      throw ArgumentError('Başlangıç, bitişten önce olmalı');
    }
  }

  int get days => end.difference(start).inDays;

  bool contains(DateTime moment) =>
      !moment.isBefore(start) && !moment.isAfter(end);

  bool overlaps(DateRange other) =>
      start.isBefore(other.end) && other.start.isBefore(end);

  /// Değiştirmiyor; uzatılmış YENİ bir aralık üretiyor.
  DateRange extendBy(Duration duration) =>
      DateRange(start: start, end: end.add(duration));

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is DateRange && other.start == start && other.end == end);

  @override
  int get hashCode => Object.hash(start, end);

  @override
  String toString() => '${_fmt(start)} → ${_fmt(end)} ($days gün)';

  static String _fmt(DateTime d) =>
      '${d.day.toString().padLeft(2, '0')}.${d.month.toString().padLeft(2, '0')}.${d.year}';
}

// ############################################################################
//
//  SEPET SATIRI — İKİ VERSİYON
//
// ############################################################################

/// IMMUTABLE satır. Miktar değişince yeni satır üretilir.
class LineItem {
  final String sku;
  final String name;
  final Money unitPrice;
  final int quantity;

  const LineItem({
    required this.sku,
    required this.name,
    required this.unitPrice,
    required this.quantity,
  });

  Money get subtotal => unitPrice * quantity;

  LineItem withQuantity(int newQuantity) {
    if (newQuantity < 1) {
      throw ArgumentError.value(newQuantity, 'newQuantity', 'En az 1 olmalı');
    }
    return LineItem(
      sku: sku,
      name: name,
      unitPrice: unitPrice,
      quantity: newQuantity,
    );
  }

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LineItem &&
          other.sku == sku &&
          other.unitPrice == unitPrice &&
          other.quantity == quantity);

  @override
  int get hashCode => Object.hash(sku, unitPrice, quantity);

  @override
  String toString() => '$name x$quantity = $subtotal';
}

/// MUTABLE satır — Bölüm 3'teki Set/Map tuzağını göstermek için.
class MutableLineItem {
  final String sku;
  final String name;
  final Money unitPrice;
  int quantity; // <-- değişebilir

  MutableLineItem({
    required this.sku,
    required this.name,
    required this.unitPrice,
    required this.quantity,
  });

  Money get subtotal => unitPrice * quantity;

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is MutableLineItem &&
          other.sku == sku &&
          other.unitPrice == unitPrice &&
          other.quantity == quantity);

  // TEHLİKE: hashCode değişebilen bir alana (quantity) dayanıyor.
  @override
  int get hashCode => Object.hash(sku, unitPrice, quantity);

  @override
  String toString() => '$name x$quantity = $subtotal';
}

// ############################################################################
//
//  BÖLÜM 1 — MUTABLE SEPET
//
//  Metotlar 'this'i değiştirir ve void döner. Kullanımı en tanıdık,
//  en kısa yazım. Sorunları Bölüm 3'te ortaya çıkacak.
//
// ############################################################################

class MutableCart {
  final List<LineItem> _items = [];
  String _couponCode = '';

  List<LineItem> get items => List.unmodifiable(_items);
  String get couponCode => _couponCode;
  bool get isEmpty => _items.isEmpty;

  Money get subtotal =>
      _items.fold(const Money.zero(), (sum, i) => sum + i.subtotal);

  Money get discount =>
      _couponCode == 'YILBASI' ? subtotal.percent(20) : const Money.zero();

  Money get total => subtotal - discount;

  // ---- MUTATOR'LAR: kendini değiştirir, bir şey döndürmez ----

  void add(LineItem item) {
    final index = _items.indexWhere((i) => i.sku == item.sku);
    if (index >= 0) {
      _items[index] = _items[index].withQuantity(
        _items[index].quantity + item.quantity,
      );
    } else {
      _items.add(item);
    }
  }

  void changeQuantity(String sku, int quantity) {
    final index = _items.indexWhere((i) => i.sku == sku);
    if (index < 0) throw ArgumentError('Sepette $sku yok');
    _items[index] = _items[index].withQuantity(quantity);
  }

  void applyCoupon(String code) => _couponCode = code;

  void removeCoupon() => _couponCode = '';

  void clear() {
    _items.clear();
    _couponCode = '';
  }

  @override
  String toString() =>
      'MutableCart(${_items.length} satır, ara $subtotal, toplam $total)';
}

// ############################################################################
//
//  BÖLÜM 2 — IMMUTABLE SEPET
//
//  Aynı işlemler, farklı imza:
//     void add(item)              ->  ImmutableCart add(item)
//     cart.add(x);                ->  cart = cart.add(x);
//
//  Bütün alanlar final. Değiştirme diye bir şey yok; yalnızca
//  "bir öncekine benzeyen yeni bir sepet üretmek" var.
//
// ############################################################################

class ImmutableCart {
  final List<LineItem> items;
  final String couponCode;

  ImmutableCart({List<LineItem> items = const [], this.couponCode = ''})
    : items = List.unmodifiable(items);

  Money get subtotal =>
      items.fold(const Money.zero(), (sum, i) => sum + i.subtotal);

  Money get discount =>
      couponCode == 'YILBASI' ? subtotal.percent(20) : const Money.zero();

  Money get total => subtotal - discount;

  bool get isEmpty => items.isEmpty;

  // ---- "MUTATOR" YERİNE ÜRETİCİLER: yeni nesne döndürür ----

  ImmutableCart add(LineItem item) {
    final index = items.indexWhere((i) => i.sku == item.sku);

    // Yeni bir liste kuruyoruz; mevcut liste hiç değişmiyor.
    final next = [...items];
    if (index >= 0) {
      next[index] = next[index].withQuantity(
        next[index].quantity + item.quantity,
      );
    } else {
      next.add(item);
    }

    return ImmutableCart(items: next, couponCode: couponCode);
  }

  ImmutableCart changeQuantity(String sku, int quantity) {
    final index = items.indexWhere((i) => i.sku == sku);
    if (index < 0) throw ArgumentError('Sepette $sku yok');

    final next = [...items];
    next[index] = next[index].withQuantity(quantity);
    return ImmutableCart(items: next, couponCode: couponCode);
  }

  ImmutableCart removeItem(String sku) => ImmutableCart(
    items: items.where((i) => i.sku != sku).toList(),
    couponCode: couponCode,
  );

  ImmutableCart withCoupon(String code) =>
      ImmutableCart(items: items, couponCode: code);

  ImmutableCart withoutCoupon() => ImmutableCart(items: items);

  /// Flutter'da state sınıflarında sürekli göreceğin kalıp.
  ImmutableCart copyWith({List<LineItem>? items, String? couponCode}) =>
      ImmutableCart(
        items: items ?? this.items,
        couponCode: couponCode ?? this.couponCode,
      );

  @override
  bool operator ==(Object other) {
    if (identical(this, other)) return true;
    if (other is! ImmutableCart) return false;
    if (other.couponCode != couponCode) return false;
    if (other.items.length != items.length) return false;
    for (var i = 0; i < items.length; i++) {
      if (other.items[i] != items[i]) return false;
    }
    return true;
  }

  @override
  int get hashCode => Object.hash(couponCode, Object.hashAll(items));

  @override
  String toString() =>
      'ImmutableCart(${items.length} satır, ara $subtotal, toplam $total)';
}

// ############################################################################
//
//  BÖLÜM 4 — ENTITY
//
//  Value object'in tam tersi:
//    1. KİMLİĞİ var (id)
//    2. Eşitlik id'ye göre, değerlere göre DEĞİL
//    3. Zaman içinde değişebilir ve yine aynı varlıktır
//
//  Ayşe e-postasını değiştirdiğinde başka biri olmuyor. Ama 10 ₺'nin
//  kuruşunu değiştirirsen o artık 10 ₺ değildir.
//
//  Ayırt etme sorusu: "İki tanesinin bütün alanları aynı olsa,
//  bunlar aynı şey midir?"
//    Evet  -> value object (Money, DateRange, adres, koordinat)
//    Hayır -> entity (müşteri, sipariş, hesap, kullanıcı)
//
// ############################################################################

class CustomerEntity {
  final String id; // kimlik: asla değişmez
  String _name;
  String _email;

  CustomerEntity({
    required this.id,
    required String name,
    required String email,
  }) : _name = name,
       _email = email;

  String get name => _name;
  String get email => _email;

  // Kontrollü mutasyon (Gün 6). Kimlik sabit, nitelikler değişebilir.
  void changeEmail(String newEmail) {
    if (!newEmail.contains('@')) {
      throw ArgumentError.value(newEmail, 'newEmail', 'Geçersiz e-posta');
    }
    _email = newEmail;
  }

  void rename(String newName) {
    if (newName.trim().isEmpty) {
      throw ArgumentError.value(newName, 'newName', 'İsim boş olamaz');
    }
    _name = newName.trim();
  }

  /// Eşitlik SADECE id'ye bakıyor.
  @override
  bool operator ==(Object other) =>
      identical(this, other) || (other is CustomerEntity && other.id == id);

  @override
  int get hashCode => id.hashCode;

  @override
  String toString() => '$_name <$_email> (#$id)';
}

// ############################################################################
//
//  YARDIMCI FONKSİYONLAR — Bölüm 3'teki tuzaklar için
//
// ############################################################################

/// Masum görünen bir fonksiyon: "kuponlu fiyat ne olurdu?"
/// Ama sepeti değiştiriyor ve geri almayı unutuyor.
Money kuponluFiyatOnizle(MutableCart cart) {
  cart.applyCoupon('YILBASI');
  return cart.total;
  // cart.removeCoupon();  <-- unutuldu. Çağıranın sepeti artık kuponlu.
}

/// Immutable versiyonda aynı hatayı yapmak MÜMKÜN DEĞİL.
/// withCoupon yeni sepet üretiyor; orijinale erişip bozamıyor.
Money kuponluFiyatOnizleGuvenli(ImmutableCart cart) {
  return cart.withCoupon('YILBASI').total;
}

// ============================================================================
// ÇALIŞTIRMA
// ============================================================================

const _klavye = LineItem(
  sku: 'KLV',
  name: 'Klavye',
  unitPrice: Money(245000),
  quantity: 1,
);
const _mouse = LineItem(
  sku: 'MSE',
  name: 'Mouse',
  unitPrice: Money(62000),
  quantity: 2,
);

Future<void> main() async {
  // ==========================================================================
  print('=== BÖLÜM 1: MUTABLE YOL ===');

  final mCart = MutableCart();
  mCart.add(_klavye);
  mCart.add(_mouse);
  mCart.changeQuantity('MSE', 3);
  mCart.applyCoupon('YILBASI');

  print('  $mCart');
  print('  Tek bir nesne var, dört kez değişti.');
  print('  Yazımı kısa: cart.add(x);');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 2: IMMUTABLE YOL ===');

  var iCart = ImmutableCart();
  iCart = iCart.add(_klavye);
  iCart = iCart.add(_mouse);
  iCart = iCart.changeQuantity('MSE', 3);
  iCart = iCart.withCoupon('YILBASI');

  print('  $iCart');
  print('  Beş ayrı nesne üretildi, biri diğerini bilmiyor.');
  print('  Yazımı biraz daha uzun: cart = cart.add(x);');
  print(
    '  Atamayı unutursan değişiklik kaybolur — ama SESSİZCE bozulma olmaz.',
  );
  print('');

  // ==========================================================================
  print('=== BÖLÜM 3.1: PAYLAŞIM TUZAĞI (ALIASING) ===');

  final musteriSepeti = MutableCart();
  musteriSepeti.add(_klavye);
  print('  Önce  : $musteriSepeti  (kupon: "${musteriSepeti.couponCode}")');

  final onizleme = kuponluFiyatOnizle(musteriSepeti);
  print('  Önizleme sonucu: $onizleme');
  print('  Sonra : $musteriSepeti  (kupon: "${musteriSepeti.couponCode}")');
  print('  <- Sadece FİYAT SORDUK, sepet değişti. Kimse fark etmez.');
  print('');

  final guvenliSepet = ImmutableCart(items: const [_klavye]);
  print('  Önce  : $guvenliSepet  (kupon: "${guvenliSepet.couponCode}")');
  final onizleme2 = kuponluFiyatOnizleGuvenli(guvenliSepet);
  print('  Önizleme sonucu: $onizleme2');
  print('  Sonra : $guvenliSepet  (kupon: "${guvenliSepet.couponCode}")');
  print('  <- Fonksiyon orijinali BOZAMAZ. Dilin kendisi engelliyor.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 3.2: SET / MAP TUZAĞI ===');

  final degisken = MutableLineItem(
    sku: 'KLV',
    name: 'Klavye',
    unitPrice: const Money(245000),
    quantity: 1,
  );

  final kume = <MutableLineItem>{degisken};
  print('  Sete eklendi. contains -> ${kume.contains(degisken)}');

  degisken.quantity = 5; // hashCode değişti

  print('  quantity 5 yapıldı.');
  print('  contains  -> ${kume.contains(degisken)}   <- FALSE, ama içeride!');
  print('  remove    -> ${kume.remove(degisken)}   <- silinemiyor');
  print('  set uzunluğu: ${kume.length}   <- eleman hâlâ orada, erişilemiyor');
  print('');
  print('  Sebep: hashCode değişebilen bir alana dayanıyordu. Nesne');
  print('  koleksiyonun yanlış kovasında kaldı. Immutable nesnede bu');
  print('  hata mümkün değil, çünkü hashCode asla değişmez.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 3.3: HATA AYIKLAMA VE GEÇMİŞ ===');

  final gecmis = <ImmutableCart>[];
  var s = ImmutableCart();
  gecmis.add(s);

  s = s.add(_klavye);
  gecmis.add(s);
  s = s.add(_mouse);
  gecmis.add(s);
  s = s.withCoupon('YILBASI');
  gecmis.add(s);
  s = s.changeQuantity('MSE', 5);
  gecmis.add(s);

  print('  Geçmiş (her adım ayrı nesne):');
  for (var i = 0; i < gecmis.length; i++) {
    print('    adım $i: ${gecmis[i].total}');
  }

  final geriAlinan = gecmis[gecmis.length - 2];
  print('  Undo -> ${geriAlinan.total}');
  print('  Immutable\'da undo = eski referansı kullanmak. Ekstra kod yok.');
  print('  Mutable\'da her adımda derin kopya almak zorunda kalırdın.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 3.4: EŞİTLİK ===');

  final a = ImmutableCart(items: const [_klavye]);
  final b = ImmutableCart(items: const [_klavye]);
  print('  İki ayrı immutable sepet, aynı içerik:');
  print('    a == b            -> ${a == b}');
  print('    identical(a, b)   -> ${identical(a, b)}');
  print(
    '  Değere göre eşitlik, Flutter\'da gereksiz rebuild\'i önlemenin yolu.',
  );

  final m1 = MutableCart()..add(_klavye);
  final m2 = MutableCart()..add(_klavye);
  print('  İki mutable sepet, aynı içerik:');
  print(
    '    m1 == m2          -> ${m1 == m2}   <- değere göre == yazmak riskli',
  );
  print(
    '  Değişebilen nesnede == \'i değere bağlarsan 3.2\'deki tuzağa düşersin.',
  );
  print('');

  // ==========================================================================
  print('=== BÖLÜM 3.5: ASYNC ARASINDA DEĞİŞME ===');

  final asyncSepet = MutableCart()..add(_klavye);
  final okunanTutar = asyncSepet.total;

  // Başka bir yerden gelen bir işlem (kullanıcı tıklaması, stream olayı)
  Future.microtask(() => asyncSepet.add(_mouse));
  await Future.delayed(const Duration(milliseconds: 10));

  print('  await ÖNCESİ okunan tutar : $okunanTutar');
  print('  await SONRASI gerçek tutar: ${asyncSepet.total}');
  print('  <- Elindeki değer bayatladı. Ödeme ekranına bunu göndersen?');
  print('  Immutable nesnede elindeki referans hep aynı anlık görüntüdür.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 4: VALUE OBJECT vs ENTITY ===');

  print('  --- Value Object (Money) ---');
  const on = Money(1000);
  const onIki = Money(1200);
  print(
    '    ${Money(1000)} == ${Money(1000)} -> ${Money(1000) == const Money(1000)}',
  );
  print('    $on + $onIki = ${on + onIki}   (yeni nesne)');
  print('    $on hâlâ $on   (değişmedi)');

  print('  --- Value Object (DateRange) ---');
  final aralik = DateRange(
    start: DateTime(2026, 1, 1),
    end: DateTime(2026, 1, 15),
  );
  final uzun = aralik.extendBy(const Duration(days: 10));
  print('    orijinal: $aralik');
  print('    uzatılan: $uzun');
  print('    orijinal bozulmadı. Kural (start<end) bir daha ihlal edilemez.');

  print('  --- Entity (CustomerEntity) ---');
  final ayse = CustomerEntity(
    id: 'C-1',
    name: 'Ayşe Kaya',
    email: 'ayse@eski.com',
  );
  final ayniAyse = CustomerEntity(
    id: 'C-1',
    name: 'Ayşe K.',
    email: 'ayse@yeni.com',
  );
  final baskaMusteri = CustomerEntity(
    id: 'C-2',
    name: 'Ayşe Kaya',
    email: 'ayse@eski.com',
  );

  print('    $ayse');
  ayse.changeEmail('ayse@yeni.com');
  print('    e-posta değişti: $ayse');
  print('    Hâlâ aynı müşteri. Kimlik değişmedi.');
  print('');
  print('    Farklı alanlar, aynı id  -> ${ayse == ayniAyse}  (aynı varlık)');
  print(
    '    Aynı alanlar, farklı id  -> ${ayse == baskaMusteri}  (farklı varlık)',
  );
  print('    Money\'de bunun tam TERSİ geçerliydi.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 5: HANGİSİNİ NE ZAMAN? ===');

  const karar = [
    ['Para, tarih, koordinat, adres, renk', 'IMMUTABLE (value object)'],
    ['State sınıfları (Bloc/Riverpod/Provider)', 'IMMUTABLE'],
    ['Fonksiyonlar arası dolaşan veri', 'IMMUTABLE'],
    ['Map anahtarı veya Set elemanı', 'IMMUTABLE (zorunlu)'],
    ['Async sınırını geçen veri', 'IMMUTABLE'],
    ['Undo/redo veya geçmiş gereken yer', 'IMMUTABLE'],
    ['Kimliği olan varlık (kullanıcı, sipariş)', 'MUTABLE (kontrollü)'],
    ['Adım adım kurulan yapı (builder)', 'MUTABLE'],
    ['Çok büyük koleksiyon, sık güncelleme', 'MUTABLE (performans)'],
    ['Sadece tek bir yerde yaşayan yerel durum', 'MUTABLE (yeterli)'],
  ];

  print('  ${'DURUM'.padRight(42)}SEÇİM');
  print('  ${'-' * 42}${'-' * 28}');
  for (final satir in karar) {
    print('  ${satir[0].padRight(42)}${satir[1]}');
  }

  print('');
  print('  Pratik varsayılan: IMMUTABLE ile başla, ölçülebilir bir sebep');
  print('  çıkınca mutable\'a geç. Ters yön daha pahalıdır — mutable bir');
  print('  nesneyi sonradan immutable yapmak, ona dokunan her yeri');
  print('  değiştirmek demektir.');
  print('');
  print('  Flutter bağlantısı:');
  print('    - const widget\'lar immutable olduğu için cache\'lenebiliyor');
  print('    - State sınıfları immutable olunca == ile ucuz karşılaştırma');
  print('      yapılıyor ve gereksiz rebuild\'ler önleniyor');
  print('    - copyWith kalıbı tam olarak Bölüm 2\'deki yaklaşımdır');
  print('    - freezed paketi bu kalıbın kodunu senin yerine üretir');
}
