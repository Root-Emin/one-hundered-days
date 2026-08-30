// ============================================================================
// GÜN 6 — ENCAPSULATION  (2/2: DIŞARIDAKİ KOD)
//
// Çalıştırmak için:  dart run gun6_main.dart
// (gun6_urun.dart ile aynı klasörde olmalı)
//
// Bu dosya "dış dünya"yı temsil ediyor. Product'ın private üyelerine
// erişimi YOK — çünkü Dart'ta '_' dosya sınırında biter.
// ============================================================================

import 'gun6_urun.dart';

void main() {
  // ==========================================================================
  // BÖLÜM 1 — KÖTÜ ÖRNEK: PUBLIC ALANLAR BOZULABİLİYOR
  // ==========================================================================
  print('=== BÖLÜM 1: PUBLIC ALANLAR (KÖTÜ) ===');

  final disaridakiEtiketler = ['kampanya'];

  final gevsek = LooseProduct(
    name: 'Kablosuz Kulaklık',
    price: 1200,
    tags: disaridakiEtiketler,
  );

  print('Başlangıç: ${gevsek.name}, ${gevsek.price} ₺, ${gevsek.tags}');

  // Hiçbir kural yok; her şey serbest.
  gevsek.name = ''; // boş isim
  gevsek.price = -999; // negatif fiyat
  gevsek.discountRate = 5.0; // %500 indirim
  disaridakiEtiketler.add('sahte-etiket'); // dışarıdaki listeyi değiştirdim

  print(
    'Bozulmuş: "${gevsek.name}", ${gevsek.price} ₺, '
    'indirimli: ${gevsek.finalPrice} ₺',
  );
  print('Etiketler: ${gevsek.tags}  <- ben nesneye hiç dokunmadım!');
  print('Sebep: constructor dışarıdaki listenin REFERANSINI sakladı.');
  print('');

  // ==========================================================================
  // BÖLÜM 2 — DIŞARIDAN YAZMA DENEMELERİ
  //
  // Aşağıdaki satırların hepsi DERLENMEZ. Denemek için yorumu kaldır.
  // Aldığın hata mesajları kapsüllemenin ne demek olduğunu en iyi anlatan
  // şey; bu yüzden gerçekten dene.
  // ==========================================================================
  print('=== BÖLÜM 2: DIŞARIDAN YAZMA DENEMELERİ ===');

  final urun = Product(
    name: 'Mekanik Klavye',
    price: 2450.00,
    tags: ['elektronik', 'ofis'],
  );

  print('Ürün: $urun');

  // urun._name = '';                 // HATA: '_name' bu kütüphanede tanımlı değil
  // urun._priceInKurus = -1;         // HATA: aynı sebep
  // urun._discountPercent = 500;     // HATA: aynı sebep
  // urun.name = 'Yeni Ad';           // HATA: setter yok, sadece getter var
  // urun.price = 1;                  // HATA: setter yok
  // urun.finalPrice = 0;             // HATA: hesaplanan getter, yazılamaz

  print('Yukarıdaki 6 satır yorumda; hiçbiri derlenmiyor.');
  print('Yani bu nesneyi bozmanın DERLEME ZAMANINDA yolu yok.');
  print('');

  // ==========================================================================
  // BÖLÜM 3 — LİSTEYİ BOZMA DENEMELERİ
  //
  // Bunlar derleniyor ama çalışma anında engelleniyor.
  // ==========================================================================
  print('=== BÖLÜM 3: KOLEKSİYONU BOZMA DENEMELERİ ===');

  print('Etiketler: ${urun.tags}');

  try {
    urun.tags.add('korsan-etiket');
  } on UnsupportedError {
    print('  tags.add()    -> engellendi (salt okunur görünüm)');
  }

  try {
    urun.tags.clear();
  } on UnsupportedError {
    print('  tags.clear()  -> engellendi');
  }

  try {
    urun.priceHistory.add(
      PriceChange(
        oldPrice: 0,
        newPrice: 0,
        changedAt: DateTime.now(),
        reason: 'sahte kayıt',
      ),
    );
  } on UnsupportedError {
    print('  history.add() -> engellendi');
  }

  // ---- Savunmacı kopyanın kanıtı ----
  final baskaBirListe = ['indirimli'];
  final urun2 = Product(name: 'Mouse Pad', price: 180, tags: baskaBirListe);

  baskaBirListe.add('sahte'); // dışarıdaki listeyi değiştiriyorum

  print('Dışarıdaki liste : $baskaBirListe');
  print('Ürünün etiketleri: ${urun2.tags}  <- etkilenmedi');
  print('Sebep: constructor List.of() ile KOPYA aldı.');
  print('');

  // ==========================================================================
  // BÖLÜM 4 — KONTROLLÜ GÜNCELLEMELER
  //
  // Değiştirmenin tek yolu metotlar; her biri kuralını uyguluyor.
  // ==========================================================================
  print('=== BÖLÜM 4: KONTROLLÜ GÜNCELLEMELER ===');

  print('--- rename() ---');
  urun.rename('Mekanik Klavye TKL');
  print('  Yeni ad: ${urun.name}');

  for (final gecersiz in ['', '   ', 'X']) {
    try {
      urun.rename(gecersiz);
      print('  "$gecersiz" -> BEKLENMEDİK: kabul edildi!');
    } on ArgumentError catch (e) {
      print('  "$gecersiz" -> reddedildi: ${e.message}');
    }
  }

  print('--- applyDiscount() ---');
  urun.applyDiscount(20);
  print('  %20 indirim: ${urun.formattedPrice}');
  print('  Kazanç: ${urun.savings.toStringAsFixed(2)} ₺');

  for (final gecersiz in [-5, 90, 1000]) {
    try {
      urun.applyDiscount(gecersiz);
      print('  %$gecersiz -> BEKLENMEDİK: kabul edildi!');
    } on ArgumentError catch (e) {
      print('  %$gecersiz -> reddedildi: ${e.message}');
    }
  }
  print('  İndirim hâlâ: %${urun.discountPercent} (bozulmadı)');

  print('--- changePrice() ---');
  urun.changePrice(2650, reason: 'Tedarikçi zammı');
  urun.changePrice(2390, reason: 'Yılbaşı kampanyası');
  print('  Güncel: ${urun.formattedPrice}');
  print('  Fiyat geçmişi:');
  for (final kayit in urun.priceHistory) {
    print('    $kayit');
  }

  try {
    urun.changePrice(3000, reason: '   ');
  } on ArgumentError catch (e) {
    print('  Sebepsiz değişiklik -> reddedildi: ${e.message}');
  }

  print('--- addTag() ---');
  urun.addTag('KLAVYE'); // büyük harf -> normalize edilecek
  urun.addTag('klavye'); // tekrar -> sessizce yok sayılacak
  urun.addTag('gaming');
  print('  Etiketler: ${urun.tags}');
  print('  "klavye" var mı: ${urun.hasTag('Klavye')}');

  try {
    urun.addTag('a');
    urun.addTag('b');
    urun.addTag('c');
  } on StateError catch (e) {
    print('  Limit aşımı -> reddedildi: ${e.message}');
  }
  print('  Son hâl: $urun');
  print('');

  // ==========================================================================
  // BÖLÜM 5 — INFORMATION HIDING
  // ==========================================================================
  print('=== BÖLÜM 5: UYGULAMA DETAYI GİZLİ ===');

  print('Dışarıdan görünen fiyat: ${urun.price} (double, TL)');
  print('İçeride nasıl saklandığı: bilmiyoruz ve bilmemize gerek yok.');
  print('');
  print('Aslında kuruş cinsinden int olarak tutuluyor. Yarın bunu');
  print('Decimal sınıfına çevirsek bu dosyada TEK SATIR değişmez —');
  print('çünkü buradaki kod hiçbir zaman iç temsile bağımlı olmadı.');
  print('');
  print('Public API sadece şunlardan ibaret:');
  print('  Okuma  : name, price, finalPrice, discountPercent, savings,');
  print(
    '           isDiscounted, formattedPrice, tags, priceHistory, hasTag()',
  );
  print('  Yazma  : rename(), applyDiscount(), removeDiscount(),');
  print('           changePrice(), addTag(), removeTag()');
  print('');
  print('Bu liste bir SÖZLEŞME. İçerisi serbestçe değişebilir,');
  print('sözleşme durduğu sürece hiçbir çağıran kod bozulmaz.');
}
