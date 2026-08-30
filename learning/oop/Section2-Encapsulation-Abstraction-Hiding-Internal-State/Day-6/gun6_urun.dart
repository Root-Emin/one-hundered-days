// ============================================================================
// GÜN 6 — ENCAPSULATION  (1/2: KÜTÜPHANE DOSYASI)
//
// Bu dosya sınıfları tanımlar. Denemeler gun6_main.dart'ta.
// Çalıştırmak için:  dart run gun6_main.dart
//
// DART'A ÖZGÜ KRİTİK BİLGİ
// Dart'ta 'private' anahtar kelimesi yoktur. '_' ile başlayan üyeler
// SINIFA değil, DOSYAYA (library) özeldir.
//
// Sonuç: kapsülleme sınırı dosya sınırıdır. Bu dosyanın içindeki kod
// _priceInKurus'a erişebilir; gun6_main.dart erişemez. Java/C#'taki
// class-level private'ten farkı budur ve çoğu kişiyi şaşırtır.
// ============================================================================

import 'dart:collection';

// ============================================================================
// KÖTÜ ÖRNEK — HER ŞEY PUBLIC
//
// Hiçbir kural yok. Dışarıdaki kod alanları istediği gibi ezebilir.
// gun6_main.dart'ta bunun ne kadar kolay bozulduğunu göreceksin.
// ============================================================================

class LooseProduct {
  String name;
  double price;
  double discountRate;
  List<String> tags;

  LooseProduct({
    required this.name,
    required this.price,
    this.discountRate = 0,
    required this.tags, // <-- dışarıdaki listenin REFERANSINI saklıyor
  });

  double get finalPrice => price * (1 - discountRate);
}

// ============================================================================
// FİYAT DEĞİŞİKLİĞİ KAYDI
//
// Bütün alanları final -> nesne IMMUTABLE. Değiştirilemeyen bir nesneyi
// dışarıya vermek güvenlidir; kimse bozamaz. Kapsüllemenin en ucuz yolu:
// değiştirilebilir durum hiç oluşturmamak.
// ============================================================================

class PriceChange {
  final double oldPrice;
  final double newPrice;
  final DateTime changedAt;
  final String reason;

  const PriceChange({
    required this.oldPrice,
    required this.newPrice,
    required this.changedAt,
    required this.reason,
  });

  double get difference => newPrice - oldPrice;
  bool get isIncrease => newPrice > oldPrice;

  @override
  String toString() {
    final ok = isIncrease ? '+' : '';
    return '${oldPrice.toStringAsFixed(2)} -> ${newPrice.toStringAsFixed(2)} '
        '($ok${difference.toStringAsFixed(2)}) — $reason';
  }
}

// ============================================================================
// İYİ ÖRNEK — KAPSÜLLENMİŞ ÜRÜN
// ============================================================================

class Product {
  // ==========================================================================
  // TASK 1 — STATE'İ PRIVATE YAP
  //
  // Hiçbir alan public değil. Dışarıdan hiçbiri doğrudan yazılamaz.
  // ==========================================================================

  String _name;

  /// INFORMATION HIDING örneği:
  /// Fiyatı içeride KURUŞ olarak (int) tutuyoruz, dışarıya TL olarak
  /// (double) veriyoruz. Sebebi: double ile para tutmak yuvarlama
  /// hatalarına yol açar (0.1 + 0.2 != 0.3).
  ///
  /// Kritik nokta: bu bir UYGULAMA DETAYI. Yarın Decimal sınıfına geçsek
  /// dışarıdaki tek bir satır bile değişmez, çünkü kimse _priceInKurus'u
  /// görmüyor. "Public API'yi bozmadan içeriyi değiştirebilmek" tam olarak
  /// kapsüllemenin kazandırdığı şey.
  int _priceInKurus;

  int _discountPercent;

  final List<String> _tags;
  final List<PriceChange> _history = [];

  // İş kuralları tek bir yerde, sabit olarak.
  static const int maxDiscountPercent = 70;
  static const int maxTags = 5;

  Product({required String name, required double price, List<String>? tags})
    : _name = name.trim(),
      _priceInKurus = (price * 100).round(),
      _discountPercent = 0,

      // ---------------------------------------------------------------
      // DEFENSIVE COPY (savunmacı kopya)
      //
      // 'List.of(...)' yeni bir liste üretir. Eğer '_tags = tags ?? []'
      // yazsaydık, çağıran taraf elindeki listeyi sonradan değiştirerek
      // nesnemizin içine müdahale edebilirdi.
      //
      // Constructor'da doğrulama yapıp sonra referansı saklamak,
      // kapsüllemede en sık yapılan sessiz hatadır.
      // ---------------------------------------------------------------
      _tags = List.of(tags ?? const []) {
    if (_name.isEmpty) {
      throw ArgumentError.value(name, 'name', 'Ürün adı boş olamaz');
    }
    if (price < 0 || price.isNaN || price.isInfinite) {
      throw ArgumentError.value(
        price,
        'price',
        'Fiyat geçerli ve pozitif olmalı',
      );
    }
    if (_tags.length > maxTags) {
      throw ArgumentError.value(
        tags,
        'tags',
        'En fazla $maxTags etiket olabilir',
      );
    }
  }

  // ==========================================================================
  // TASK 4 — GÜVENLİ OKUMA ERİŞİMİ
  //
  // Getter'lar okuma izni verir, yazma izni vermez. Setter yazmadığımız
  // sürece 'product.name = "x"' derlenmez.
  // ==========================================================================

  String get name => _name;

  /// Kuruş dışarı sızmıyor; dışarısı TL görüyor.
  double get price => _priceInKurus / 100;

  int get discountPercent => _discountPercent;

  bool get isDiscounted => _discountPercent > 0;

  /// COMPUTED (hesaplanan) getter — saklanmıyor, her çağrıda hesaplanıyor.
  /// Bu yüzden asla güncel olmayan bir indirimli fiyat göremezsin.
  double get finalPrice {
    final discounted = (_priceInKurus * (100 - _discountPercent)) / 100;
    return discounted.round() / 100;
  }

  double get savings => price - finalPrice;

  String get formattedPrice {
    if (!isDiscounted) return '${price.toStringAsFixed(2)} ₺';
    return '${finalPrice.toStringAsFixed(2)} ₺ '
        '(${price.toStringAsFixed(2)} ₺ yerine, %$_discountPercent indirim)';
  }

  /// ---------------------------------------------------------------------
  /// KOLEKSİYONLARI DIŞARI VERMENİN DOĞRU YOLU
  ///
  /// 'return _tags;' yazsaydık, çağıran taraf iç listemizin ta kendisini
  /// eline alırdı ve tags.add('sahte') diyerek maxTags kuralını
  /// atlayabilirdi. Getter var diye kapsülleme sağlanmıyor.
  ///
  /// UnmodifiableListView okumaya izin verir, yazma denemesinde
  /// çalışma anında hata fırlatır.
  /// ---------------------------------------------------------------------
  UnmodifiableListView<String> get tags => UnmodifiableListView(_tags);

  UnmodifiableListView<PriceChange> get priceHistory =>
      UnmodifiableListView(_history);

  bool hasTag(String tag) => _tags.contains(tag.toLowerCase().trim());

  // ==========================================================================
  // TASK 3 — KONTROLLÜ GÜNCELLEMELER
  //
  // Durum değişikliğinin TEK yolu bu metotlar. Her biri kendi kuralını
  // uyguluyor. Yani "geçersiz Product" diye bir şey oluşamıyor —
  // ne doğum anında (Gün 3) ne de sonrasında.
  // ==========================================================================

  void rename(String newName) {
    final trimmed = newName.trim();

    if (trimmed.isEmpty) {
      throw ArgumentError.value(newName, 'newName', 'Yeni ad boş olamaz');
    }
    if (trimmed.length < 2) {
      throw ArgumentError.value(
        newName,
        'newName',
        'Ad en az 2 karakter olmalı',
      );
    }
    if (trimmed == _name) {
      return; // aynı isim, iş yok
    }

    _name = trimmed;
  }

  void applyDiscount(int percent) {
    if (percent < 0) {
      throw ArgumentError.value(percent, 'percent', 'İndirim negatif olamaz');
    }
    if (percent > maxDiscountPercent) {
      throw ArgumentError.value(
        percent,
        'percent',
        'İndirim en fazla %$maxDiscountPercent olabilir',
      );
    }
    _discountPercent = percent;
  }

  void removeDiscount() {
    _discountPercent = 0;
  }

  /// Fiyat değişikliği sadece buradan yapılabilir ve otomatik olarak
  /// geçmişe kaydedilir. Alan public olsaydı bu kaydı tutmanın hiçbir
  /// yolu olmazdı — biri fiyatı değiştirir, kimsenin haberi olmazdı.
  void changePrice(double newPrice, {required String reason}) {
    if (newPrice < 0 || newPrice.isNaN || newPrice.isInfinite) {
      throw ArgumentError.value(
        newPrice,
        'newPrice',
        'Geçerli ve pozitif olmalı',
      );
    }
    if (reason.trim().isEmpty) {
      throw ArgumentError.value(reason, 'reason', 'Değişiklik sebebi zorunlu');
    }

    final old = price;
    _priceInKurus = (newPrice * 100).round();

    _history.add(
      PriceChange(
        oldPrice: old,
        newPrice: price,
        changedAt: DateTime.now(),
        reason: reason.trim(),
      ),
    );
  }

  void addTag(String tag) {
    final normalized = tag.toLowerCase().trim();

    if (normalized.isEmpty) {
      throw ArgumentError.value(tag, 'tag', 'Etiket boş olamaz');
    }
    if (_tags.length >= maxTags) {
      throw StateError('En fazla $maxTags etiket eklenebilir');
    }
    if (_tags.contains(normalized)) {
      return; // zaten var
    }

    _tags.add(normalized);
  }

  bool removeTag(String tag) => _tags.remove(tag.toLowerCase().trim());

  @override
  String toString() {
    final tagText = _tags.isEmpty ? '' : ' [${_tags.join(', ')}]';
    return '$_name — $formattedPrice$tagText';
  }
}
