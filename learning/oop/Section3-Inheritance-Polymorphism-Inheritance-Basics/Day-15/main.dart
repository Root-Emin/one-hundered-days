// ============================================================================
// GÜN 15 — PRATİK: SÖZLEŞME + POLYMORPHISM + KOMPOZİSYON  (Dart)
//
// Çalıştırmak için:  dart run gun15_sekil_tasarimi.dart
// veya dartpad.dev'e yapıştır.
//
// Bu gün yeni kavram öğretmiyor; Gün 11-14'ü tek bir çalışan tasarımda
// birleştiriyor.
//
// SENARYO: Bir zemin kaplama hesap aracı. Farklı şekillerdeki alanların
// metrekaresini bulup malzeme maliyetini çıkarıyoruz.
//
// BÖLÜM 1 -> Sözleşme ve üç temel implementasyon
// BÖLÜM 2 -> Polymorphic collection
// BÖLÜM 3 -> Kompozisyon 1: şekilleri birleştirmek ve sarmalamak
// BÖLÜM 4 -> Kompozisyon 2: davranışı strateji olarak paylaşmak
// BÖLÜM 5 -> Critique: kasten kaçınılan kalıtım (Square/Rectangle)
// BÖLÜM 6 -> Tasarım değerlendirmesi
// ============================================================================

import 'dart:math' as math;

// ############################################################################
//  PARA (Gün 9'dan tanıdık value object)
// ############################################################################

class Money {
  final int kurus;

  const Money(this.kurus);
  static const zero = Money(0);

  factory Money.lira(double amount) => Money((amount * 100).round());

  double get lira => kurus / 100;

  Money operator +(Money other) => Money(kurus + other.kurus);
  Money operator *(num factor) => Money((kurus * factor).round());
  bool operator >(Money other) => kurus > other.kurus;

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
//  BÖLÜM 1 — SÖZLEŞME
//
//  Üç operasyon. Hepsi bu kadar.
//
//  Sözleşmede 'radius', 'width', 'vertices' gibi hiçbir şey yok —
//  bunlar implementasyon detayı. Sözleşme sadece "her şeklin bir adı,
//  bir alanı ve bir çevresi vardır" diyor.
//
//  Bu sözleşme kararlı: yarın altıgen eklesek de değişmez.
//
// ############################################################################

abstract interface class Shape {
  String get name;
  double get area; // m²
  double get perimeter; // m
}

/// -------------------------------------------------------------------------
/// IMPLEMENTASYON 1: Daire
/// -------------------------------------------------------------------------
class Circle implements Shape {
  final double radius;

  Circle(this.radius) {
    if (radius <= 0) {
      throw ArgumentError.value(radius, 'radius', 'Pozitif olmalı');
    }
  }

  @override
  String get name => 'Daire (r=$radius)';

  @override
  double get area => math.pi * radius * radius;

  @override
  double get perimeter => 2 * math.pi * radius;
}

/// -------------------------------------------------------------------------
/// IMPLEMENTASYON 2: Dikdörtgen
///
/// IMMUTABLE: width ve height final. Bölüm 5'te bunun neden kritik
/// olduğunu göreceksin.
/// -------------------------------------------------------------------------
class Rectangle implements Shape {
  final double width;
  final double height;

  Rectangle({required this.width, required this.height}) {
    if (width <= 0 || height <= 0) {
      throw ArgumentError('Kenarlar pozitif olmalı ($width x $height)');
    }
  }

  @override
  String get name => 'Dikdörtgen (${width}x$height)';

  @override
  double get area => width * height;

  @override
  double get perimeter => 2 * (width + height);
}

/// -------------------------------------------------------------------------
/// IMPLEMENTASYON 3: Üçgen
///
/// Üç kenardan Heron formülüyle alan. Üçgen eşitsizliği constructor'da
/// kontrol ediliyor (Gün 3 + Gün 7): geçersiz üçgen doğamıyor.
/// -------------------------------------------------------------------------
class Triangle implements Shape {
  final double a;
  final double b;
  final double c;

  Triangle({required this.a, required this.b, required this.c}) {
    if (a <= 0 || b <= 0 || c <= 0) {
      throw ArgumentError('Kenarlar pozitif olmalı');
    }
    if (a + b <= c || a + c <= b || b + c <= a) {
      throw ArgumentError('Üçgen eşitsizliği sağlanmıyor ($a, $b, $c)');
    }
  }

  @override
  String get name => 'Üçgen ($a-$b-$c)';

  @override
  double get perimeter => a + b + c;

  @override
  double get area {
    final s = perimeter / 2;
    return math.sqrt(s * (s - a) * (s - b) * (s - c));
  }
}

/// -------------------------------------------------------------------------
/// IMPLEMENTASYON 4: Kare
///
/// DİKKAT: 'extends Rectangle' DEĞİL. Kendi başına bir Shape.
/// Gerekçesi Bölüm 5'te.
/// -------------------------------------------------------------------------
class Square implements Shape {
  final double side;

  Square(this.side) {
    if (side <= 0) {
      throw ArgumentError.value(side, 'side', 'Pozitif olmalı');
    }
  }

  @override
  String get name => 'Kare ($side)';

  @override
  double get area => side * side;

  @override
  double get perimeter => 4 * side;

  /// Dikdörtgene ihtiyaç duyan bir yer varsa dönüşüm AÇIK olsun.
  /// Örtük bir alt tür ilişkisi kurmaktan iyidir.
  Rectangle toRectangle() => Rectangle(width: side, height: side);
}

// ############################################################################
//
//  BÖLÜM 3 — KOMPOZİSYON 1: ŞEKİLLERİ BİRLEŞTİRMEK
//
//  Aşağıdaki iki sınıf Shape'i hem UYGULUYOR hem İÇİNDE TUTUYOR.
//  Gün 14'teki sarmalayıcı fikrinin aynısı, farklı domain.
//
//  Sonuç: bir grup şekil, tek bir şekil gibi kullanılabiliyor.
//  Çağıran kod aradaki farkı göremiyor.
//
// ############################################################################

/// Birden çok şekli tek şekil gibi ele alır (Composite deseni).
class CompositeShape implements Shape {
  final String label;
  final List<Shape> _parts;

  CompositeShape(this.label, List<Shape> parts) : _parts = List.of(parts) {
    if (_parts.isEmpty) {
      throw ArgumentError('En az bir parça olmalı');
    }
  }

  List<Shape> get parts => List.unmodifiable(_parts);

  @override
  String get name => '$label (${_parts.length} parça)';

  @override
  double get area => _parts.fold(0.0, (sum, s) => sum + s.area);

  @override
  double get perimeter => _parts.fold(0.0, (sum, s) => sum + s.perimeter);
}

/// Bir şekli ölçeklenmiş haliyle sunar.
///
/// Matematiksel not: k katına büyütünce çevre k, alan k² katına çıkar.
/// Bu kural HER şekil için geçerli olduğundan, sarmalayıcı içindeki
/// şeklin ne olduğunu bilmeden doğru sonucu üretebiliyor.
class ScaledShape implements Shape {
  final Shape _inner;
  final double factor;

  ScaledShape(this._inner, this.factor) {
    if (factor <= 0) {
      throw ArgumentError.value(factor, 'factor', 'Pozitif olmalı');
    }
  }

  @override
  String get name => '${_inner.name} ×$factor';

  @override
  double get area => _inner.area * factor * factor;

  @override
  double get perimeter => _inner.perimeter * factor;
}

/// Bir şeklin içinden başka bir şekli çıkarır (havuzlu teras, sütunlu salon).
class ShapeWithHole implements Shape {
  final Shape _outer;
  final Shape _hole;

  ShapeWithHole({required Shape outer, required Shape hole})
    : _outer = outer,
      _hole = hole {
    if (hole.area >= outer.area) {
      throw ArgumentError('Boşluk, dış şekilden büyük olamaz');
    }
  }

  @override
  String get name => '${_outer.name} − ${_hole.name}';

  @override
  double get area => _outer.area - _hole.area;

  /// Kaplama işinde iç kenar da kesilir, o yüzden çevreler toplanır.
  @override
  double get perimeter => _outer.perimeter + _hole.perimeter;
}

// ############################################################################
//
//  BÖLÜM 4 — KOMPOZİSYON 2: DAVRANIŞI STRATEJİ OLARAK PAYLAŞMAK
//
//  Biçimlendirme ve fiyatlandırma, şeklin kendi işi değil.
//  Bunları Shape'e koymak ya da bir BaseShape'ten miras vermek yerine
//  ayrı nesnelere aldık.
//
// ############################################################################

/// PAYLAŞILAN DAVRANIŞ 1: biçimlendirme.
///
/// Bir 'abstract class BaseShape' yazıp describe()'ı oraya koyabilirdik.
/// Koymadık — çünkü o zaman her şekil BaseShape'ten türemek zorunda
/// kalırdı ve ScaledShape gibi sarmalayıcılar için bu tuhaf olurdu.
/// Kompozisyonla aynı davranışı HERKES kullanabiliyor.
class ShapeFormatter {
  final int decimals;
  const ShapeFormatter({this.decimals = 2});

  String describe(Shape shape) {
    final alan = shape.area.toStringAsFixed(decimals);
    final cevre = shape.perimeter.toStringAsFixed(decimals);
    return '${shape.name.padRight(30)} alan $alan m²   çevre $cevre m';
  }
}

/// PAYLAŞILAN DAVRANIŞ 2: fiyatlandırma stratejisi.
abstract interface class PricingPolicy {
  String get name;
  Money priceFor(double areaM2);
}

class FlatRatePricing implements PricingPolicy {
  final Money perSquareMeter;
  const FlatRatePricing(this.perSquareMeter);

  @override
  String get name => 'Sabit fiyat (${perSquareMeter}/m²)';

  @override
  Money priceFor(double areaM2) => perSquareMeter * areaM2;
}

class TieredPricing implements PricingPolicy {
  final Money base;
  final double thresholdM2;
  final int discountPercent;

  const TieredPricing({
    required this.base,
    this.thresholdM2 = 50,
    this.discountPercent = 15,
  });

  @override
  String get name =>
      'Kademeli ($thresholdM2 m² üstü %$discountPercent indirim)';

  @override
  Money priceFor(double areaM2) {
    if (areaM2 <= thresholdM2) return base * areaM2;
    final normal = base * thresholdM2;
    final indirimli =
        base * (areaM2 - thresholdM2) * ((100 - discountPercent) / 100);
    return normal + indirimli;
  }
}

/// SARMALAYICI: başka bir politikayı kuşatıp alt sınır uyguluyor.
/// PricingPolicy'yi hem uyguluyor hem içinde tutuyor — Gün 14 deseni.
class MinimumChargePricing implements PricingPolicy {
  final PricingPolicy _inner;
  final Money minimum;

  const MinimumChargePricing(this._inner, {required this.minimum});

  @override
  String get name => '${_inner.name} + min $minimum';

  @override
  Money priceFor(double areaM2) {
    final hesaplanan = _inner.priceFor(areaM2);
    return minimum > hesaplanan ? minimum : hesaplanan;
  }
}

/// Şekli ve fiyat politikasını birleştiren üst seviye nesne.
/// İkisini de DIŞARIDAN alıyor (Gün 13: dependency inversion).
class CostEstimate {
  final Shape shape;
  final PricingPolicy policy;
  final Money laborPerMeter;

  const CostEstimate({
    required this.shape,
    required this.policy,
    this.laborPerMeter = const Money(4500),
  });

  Money get materialCost => policy.priceFor(shape.area);
  Money get trimCost => laborPerMeter * shape.perimeter;
  Money get total => materialCost + trimCost;
}

// ############################################################################
//
//  BÖLÜM 5 — CRITIQUE: KASTEN KAÇINILAN KALITIM
//
//  "Kare bir dikdörtgendir" — geometride doğru, kodda tuzak.
//  Aşağıdaki iki sınıf tuzağı çalışır halde gösteriyor.
//
// ############################################################################

/// Değiştirilebilir dikdörtgen. Sözü şu: genişliği değiştirmek
/// yüksekliği etkilemez.
class MutableRectangle {
  double width;
  double height;

  MutableRectangle({required this.width, required this.height});

  double get area => width * height;

  @override
  String toString() => '${width}x$height (alan ${area.toStringAsFixed(0)})';
}

/// "Kare bir dikdörtgendir" diyerek extends ettik.
/// Kare olma özelliğini korumak için iki kenarı birlikte değiştiriyoruz.
/// Mantıklı görünüyor. Ama base'in SÖZÜNÜ bozduk.
class BadSquare extends MutableRectangle {
  BadSquare(double side) : super(width: side, height: side);

  @override
  set width(double value) {
    super.width = value;
    super.height = value; // yan etki: yükseklik de değişti
  }

  @override
  set height(double value) {
    super.width = value;
    super.height = value;
  }
}

// ============================================================================
// ÇALIŞTIRMA
// ============================================================================

const _formatter = ShapeFormatter();

void main() {
  // ==========================================================================
  print('=== BÖLÜM 1: SÖZLEŞME VE IMPLEMENTASYONLAR ===');

  final temeller = <Shape>[
    Circle(2.5),
    Rectangle(width: 6, height: 4),
    Triangle(a: 3, b: 4, c: 5),
    Square(3),
  ];

  for (final s in temeller) {
    print('  ${_formatter.describe(s)}');
  }
  print('');

  print('  Geçersiz şekiller doğamıyor:');
  final denemeler = <String, Shape Function()>{
    'Negatif yarıçap': () => Circle(-1),
    'Sıfır kenar': () => Rectangle(width: 0, height: 5),
    'Üçgen eşitsizliği': () => Triangle(a: 1, b: 2, c: 10),
  };
  denemeler.forEach((etiket, uret) {
    try {
      uret();
      print('    $etiket -> BEKLENMEDİK: üretildi!');
    } on ArgumentError catch (e) {
      print('    $etiket -> reddedildi: ${e.message}');
    }
  });
  print('');

  // ==========================================================================
  print('=== BÖLÜM 3: KOMPOZİSYONLA KURULAN ŞEKİLLER ===');

  // Salon + koridor + mutfak birleşimi
  final zemin = CompositeShape('Salon katı', [
    Rectangle(width: 6, height: 4),
    Rectangle(width: 1.2, height: 5),
    Triangle(a: 3, b: 4, c: 5),
  ]);

  // Havuzlu teras: dış dikdörtgenden daireyi çıkar
  final teras = ShapeWithHole(
    outer: Rectangle(width: 8, height: 5),
    hole: Circle(1.5),
  );

  // Aynı plan, 1.5 katı ölçekte
  final buyutulmus = ScaledShape(zemin, 1.5);

  // Sarmalayıcılar iç içe geçebiliyor
  final karmasik = CompositeShape('Tüm proje', [
    zemin,
    teras,
    ScaledShape(Square(2), 2),
  ]);

  for (final s in [zemin, teras, buyutulmus, karmasik]) {
    print('  ${_formatter.describe(s)}');
  }
  print('');

  print('  Ölçekleme kuralı: çevre ×k, alan ×k²');
  print('    zemin alan       : ${zemin.area.toStringAsFixed(2)}');
  print('    ×1.5 alan        : ${buyutulmus.area.toStringAsFixed(2)}');
  print(
    '    oran             : ${(buyutulmus.area / zemin.area).toStringAsFixed(2)}'
    '  (1.5² = 2.25)',
  );
  print('  ScaledShape içindeki şeklin ne olduğunu BİLMİYOR;');
  print('  kural her şekil için aynı olduğu için bilmesine gerek yok.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 2: POLYMORPHIC COLLECTION ===');

  // Tipi List<Shape>. İçinde yedi FARKLI sınıf var:
  // Circle, Rectangle, Triangle, Square, CompositeShape,
  // ScaledShape, ShapeWithHole.
  final hepsi = <Shape>[...temeller, zemin, teras, buyutulmus];

  print('  Liste tipi: List<Shape>, içinde ${hepsi.length} nesne,');
  print('  yedi farklı sınıftan. Aşağıdaki hesaplar hiçbirinin');
  print('  adını bilmiyor — tek bir "is" kontrolü yok.');
  print('');

  final toplamAlan = hepsi.fold<double>(0, (sum, s) => sum + s.area);
  final toplamCevre = hepsi.fold<double>(0, (sum, s) => sum + s.perimeter);

  print('  Toplam alan  : ${toplamAlan.toStringAsFixed(2)} m²');
  print('  Toplam çevre : ${toplamCevre.toStringAsFixed(2)} m');
  print(
    '  Ortalama alan: ${(toplamAlan / hepsi.length).toStringAsFixed(2)} m²',
  );

  final sirali = [...hepsi]..sort((a, b) => b.area.compareTo(a.area));
  print('  En büyük üç alan:');
  for (final s in sirali.take(3)) {
    print('    ${_formatter.describe(s)}');
  }

  final buyukler = hepsi.where((s) => s.area > 20).toList();
  print('  20 m²\'den büyük ${buyukler.length} şekil var.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 4: STRATEJİLERLE MALİYET ===');

  final politikalar = <PricingPolicy>[
    FlatRatePricing(Money.lira(320)),
    const TieredPricing(base: Money(32000), thresholdM2: 40),
    MinimumChargePricing(
      FlatRatePricing(Money.lira(320)),
      minimum: Money.lira(15000),
    ),
  ];

  print('  Şekil: ${zemin.name}, ${zemin.area.toStringAsFixed(2)} m²');
  print('');
  for (final p in politikalar) {
    final tahmin = CostEstimate(shape: zemin, policy: p);
    print('  ${p.name}');
    print('    malzeme: ${tahmin.materialCost}');
    print('    süpürgelik: ${tahmin.trimCost}');
    print('    TOPLAM: ${tahmin.total}');
  }
  print('');

  print('  Küçük bir alanda minimum ücret devreye giriyor:');
  final kucuk = Square(1.5);
  for (final p in [politikalar.first, politikalar.last]) {
    print('    ${p.name}');
    print('      ${CostEstimate(shape: kucuk, policy: p).materialCost}');
  }
  print('');
  print('  MinimumChargePricing başka bir politikayı SARMALIYOR.');
  print('  Politikaları da şekiller gibi birleştirebiliyoruz.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 5: CRITIQUE — KASTEN KAÇINILAN KALITIM ===');

  print('  "Kare bir dikdörtgendir" cümlesi geometride doğru.');
  print('  Kodda ne oluyor:');
  print('');

  // Bu fonksiyon MutableRectangle'ın sözüne güveniyor:
  // "genişliği değiştirmek yüksekliği etkilemez"
  void enBoyAyarla(MutableRectangle r) {
    r.width = 5;
    r.height = 4;
    print(
      '    5x4 ayarladım, beklenen alan 20 -> gerçek alan ${r.area.toStringAsFixed(0)}',
    );
  }

  print('  Normal dikdörtgenle:');
  enBoyAyarla(MutableRectangle(width: 1, height: 1));

  print('  BadSquare ile (aynı fonksiyon, aynı tip):');
  enBoyAyarla(BadSquare(1));

  print('');
  print('  Fonksiyon bozulmadı, base bozulmadı — ama BİRLİKTE yanlışlar.');
  print('  BadSquare, base\'in "kenarlar bağımsızdır" sözünü tutmuyor.');
  print('  Gün 12\'deki Liskov kuralının ihlali bu.');
  print('');

  print('  --- BİZİM TASARIMDA NEDEN BU SORUN YOK ---');
  print('  1. Square, Rectangle\'dan türemiyor. İkisi de bağımsız Shape.');
  print('     Yanlış yere geçirilme ihtimali derleme zamanında yok.');
  print('  2. Şekillerimiz IMMUTABLE. Bir kenarı sonradan değiştirme');
  print('     diye bir şey yok, dolayısıyla bozulacak bir söz de yok.');
  print('     (Gün 9: değişmezlik, invariant korumanın en ucuz yolu.)');
  print('  3. Dönüşüm gerekiyorsa açıkça yapılıyor: Square.toRectangle()');
  final k = Square(3);
  print('     ${_formatter.describe(k)}');
  print('     ${_formatter.describe(k.toRectangle())}');
  print('');
  print('  İlginç sonuç: LSP ihlalinin kaynağı "kare" değil,');
  print('  DEĞİŞTİRİLEBİLİRLİK. Rectangle immutable olsaydı Square onun');
  print('  alt türü olabilirdi ve hiçbir sorun çıkmazdı.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 6: TASARIM DEĞERLENDİRMESİ ===');

  print('  --- Hiyerarşi derinliği ---');
  print('    Shape (sözleşme)');
  print('      ├── Circle, Rectangle, Triangle, Square      (1 seviye)');
  print('      └── CompositeShape, ScaledShape, ShapeWithHole (1 seviye)');
  print('    PricingPolicy (sözleşme)');
  print('      └── FlatRate, Tiered, MinimumCharge          (1 seviye)');
  print('');
  print('    Hiçbir yerde 2. seviye yok. Bütün "derinlik" ihtiyacı');
  print('    kalıtımla değil, İÇ İÇE GEÇMEYLE karşılanıyor:');
  print('    ScaledShape(CompositeShape([ShapeWithHole(...), ...]), 1.5)');
  print('');

  print('  --- Kontrol listesi ---');
  const kontroller = [
    ['Sözleşmede implementasyon detayı var mı?', 'Hayır'],
    ['Her sınıf tek cümleyle anlatılabiliyor mu?', 'Evet'],
    ['Geçersiz nesne üretilebiliyor mu?', 'Hayır'],
    ['Polymorphic döngüde "is" kontrolü var mı?', 'Hayır'],
    ['Ortak davranış kalıtımla mı paylaşıldı?', 'Hayır, kompozisyonla'],
    ['Hiyerarşi 2 seviyeyi aşıyor mu?', 'Hayır'],
    ['Yeni şekil eklemek mevcut kodu bozar mı?', 'Hayır'],
    ['Yeni fiyat politikası mevcut kodu bozar mı?', 'Hayır'],
  ];
  for (final k in kontroller) {
    print('    ${k[0].padRight(46)}${k[1]}');
  }

  print('');
  print('  --- Bu tasarımda hangi gün nerede ---');
  const izler = [
    ['Gün 11', 'Kalıtımı sığ tuttuk; sadece sözleşme kullandık'],
    ['Gün 12', 'Polymorphic döngü: List<Shape> üzerinde tek geçiş'],
    ['Gün 13', 'Shape ve PricingPolicy birer sözleşme; DI ile verildi'],
    ['Gün 14', 'ScaledShape/CompositeShape/MinimumCharge birer sarmalayıcı'],
    ['Gün 9', 'Bütün şekiller immutable; LSP tuzağı bu yüzden yok'],
    ['Gün 7', 'Üçgen eşitsizliği ve pozitif kenar constructor\'da'],
  ];
  for (final i in izler) {
    print('    ${i[0].padRight(8)}${i[1]}');
  }

  print('');
  print('  Genişletme testi: altıgen eklemek istesek Shape\'i uygulayan');
  print('  tek bir sınıf yazmamız yeterli. CompositeShape, ScaledShape,');
  print('  CostEstimate, sıralama ve toplama kodlarının hiçbirine');
  print('  dokunmayız. Sözleşmeye programlamanın somut karşılığı bu.');
}
