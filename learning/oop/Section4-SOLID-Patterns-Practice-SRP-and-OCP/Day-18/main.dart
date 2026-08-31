// ============================================================================
// GÜN 18 — FACTORY & STRATEGY  (Dart)
//
// Çalıştırmak için:  dart run gun18_factory_ve_strategy.dart
// veya dartpad.dev'e yapıştır.
//
// BÖLÜM 1 -> Factory: nesne üretimini tek yere toplamak
// BÖLÜM 2 -> Strategy: değiştirilebilir algoritmaları arayüz arkasına almak
// BÖLÜM 3 -> Replace Conditionals: switch/if zincirini strategy'ye çevirmek
// BÖLÜM 4 -> Don't Pattern-Hunt: pattern'in fazla geldiği durumlar
//
// NOT: 'abstract interface class' Dart 3 sözdizimidir. SDK'n eskiyse
// sadece 'abstract class' yaz; anlam aynı kalır.
// ============================================================================

void main() {
  _baslik('BÖLÜM 1 — FACTORY');
  bolum1Factory();

  _baslik('BÖLÜM 2 — STRATEGY');
  bolum2Strategy();

  _baslik('BÖLÜM 3 — REPLACE CONDITIONALS');
  bolum3KosullariDegistir();

  _baslik('BÖLÜM 4 — DON\'T PATTERN-HUNT');
  bolum4PatternAvlamaAma();
}

void _baslik(String s) => print('\n${'=' * 68}\n$s\n${'=' * 68}');

// ============================================================================
// BÖLÜM 1 — FACTORY
//
// Problem: Kullanıcıyı Firestore'dan gelen Map'ten üretiyoruz. Rol'e göre
// farklı sınıf lazım. Bu "hangi sınıfı kuracağım?" kararı kod tabanına
// dağılırsa, her yeni rol eklemede N tane yeri bulup güncellemen gerekir —
// ve birini kaçırırsın.
// ============================================================================

/// Ortak taban. Gün 8'deki "kararlı soyutlama" fikri: çağıran kod
/// Ogrenci/Veli/Ogretmen değil, Kullanici görür.
abstract class Kullanici {
  final String id;
  final String ad;

  const Kullanici({required this.id, required this.ad});

  /// Alt sınıflar kendi panel başlığını kendi bilir (Tell, Don't Ask — Gün 4).
  String get panelBasligi;

  @override
  String toString() => '$runtimeType(id: $id, ad: $ad)';
}

class Ogrenci extends Kullanici {
  final String sinif;
  const Ogrenci({required super.id, required super.ad, required this.sinif});

  @override
  String get panelBasligi => 'Öğrenci Paneli — $sinif';
}

class Veli extends Kullanici {
  final List<String> cocukIdleri;
  const Veli({required super.id, required super.ad, required this.cocukIdleri});

  @override
  String get panelBasligi =>
      'Veli Paneli — ${cocukIdleri.length} öğrenci takipte';
}

class Ogretmen extends Kullanici {
  final String brans;
  const Ogretmen({required super.id, required super.ad, required this.brans});

  @override
  String get panelBasligi => 'Öğretmen Paneli — $brans';
}

// ----------------------------------------------------------------------------
// KÖTÜ HÂL: üretim kararı iki ayrı ekrana kopyalanmış.
// İkisi de aynı if-zincirini tekrar ediyor; ikincisi 'veli' rolünü unutmuş
// ve ayrıca doğrulama yapmıyor. Bu, gerçek hayatta böyle bulunur.
// ----------------------------------------------------------------------------

Kullanici? girisEkraniKotu(Map<String, dynamic> veri) {
  if (veri['rol'] == 'ogrenci') {
    return Ogrenci(id: veri['id'], ad: veri['ad'], sinif: veri['sinif']);
  } else if (veri['rol'] == 'veli') {
    return Veli(id: veri['id'], ad: veri['ad'], cocukIdleri: veri['cocuklar']);
  } else if (veri['rol'] == 'ogretmen') {
    return Ogretmen(id: veri['id'], ad: veri['ad'], brans: veri['brans']);
  }
  return null;
}

Kullanici? profilEkraniKotu(Map<String, dynamic> veri) {
  // Aynı bilgi ikinci kez yazılmış. 'veli' dalı düşmüş -> veli girince null.
  if (veri['rol'] == 'ogrenci') {
    return Ogrenci(id: veri['id'], ad: veri['ad'], sinif: veri['sinif']);
  } else if (veri['rol'] == 'ogretmen') {
    return Ogretmen(id: veri['id'], ad: veri['ad'], brans: veri['brans']);
  }
  return null; // sessiz hata
}

// ----------------------------------------------------------------------------
// İYİ HÂL: üretim kararı TEK yerde.
//
// Dikkat: burada da bir switch var — ve bu sorun değil. Factory'nin işi zaten
// "hangi sınıf?" kararını üstlenmek. Amaç switch'i yok etmek değil, onu
// kod tabanında tek bir yere hapsetmek.
// ----------------------------------------------------------------------------

class KullaniciFactory {
  const KullaniciFactory._(); // örneklenmesin; sadece statik giriş noktası

  static Kullanici fromMap(Map<String, dynamic> veri) {
    // Doğrulama da tek yerde (Gün 7: fail-fast).
    final id = (veri['id'] as String?)?.trim();
    final ad = (veri['ad'] as String?)?.trim();
    final rol = (veri['rol'] as String?)?.trim().toLowerCase();

    if (id == null || id.isEmpty) {
      throw ArgumentError('Kullanıcı id boş olamaz.');
    }
    if (ad == null || ad.isEmpty) {
      throw ArgumentError('Kullanıcı adı boş olamaz.');
    }

    switch (rol) {
      case 'ogrenci':
        return Ogrenci(
          id: id,
          ad: ad,
          sinif: (veri['sinif'] as String?) ?? 'Sınıf atanmadı',
        );
      case 'veli':
        return Veli(
          id: id,
          ad: ad,
          cocukIdleri: List<String>.from(veri['cocuklar'] as List? ?? const []),
        );
      case 'ogretmen':
        return Ogretmen(
          id: id,
          ad: ad,
          brans: (veri['brans'] as String?) ?? 'Branş atanmadı',
        );
      default:
        // Sessizce null dönmek yerine yüksek sesle patla.
        throw ArgumentError('Bilinmeyen rol: "$rol"');
    }
  }
}

// ----------------------------------------------------------------------------
// Dart'ın DİLE GÖMÜLÜ factory'si: 'factory' constructor.
// Normal (generative) constructor her çağrıda YENİ nesne üretmek zorundadır.
// factory constructor ise mevcut bir nesneyi geri verebilir veya alt tip
// döndürebilir. Gün 3'te gördüğün named constructor'dan farkı budur.
// ----------------------------------------------------------------------------

class Puan {
  final int deger;

  const Puan._(this.deger);

  static const Puan _sifir = Puan._(0);
  static const Puan _tam = Puan._(100);

  factory Puan(int deger) {
    if (deger < 0 || deger > 100) {
      throw RangeError('Puan 0-100 aralığında olmalı, gelen: $deger');
    }
    if (deger == 0) return _sifir; // aynı nesne tekrar kullanılıyor
    if (deger == 100) return _tam;
    return Puan._(deger);
  }

  @override
  String toString() => 'Puan($deger)';
}

void bolum1Factory() {
  final veliVerisi = <String, dynamic>{
    'id': 'u-42',
    'ad': 'Ayşe Yılmaz',
    'rol': 'veli',
    'cocuklar': ['u-90', 'u-91'],
  };

  print('Kötü hâl (giriş ekranı) : ${girisEkraniKotu(veliVerisi)}');
  print(
    'Kötü hâl (profil ekranı): ${profilEkraniKotu(veliVerisi)}  <-- veli kayboldu',
  );

  final kullanici = KullaniciFactory.fromMap(veliVerisi);
  print('Factory ile             : $kullanici');
  print('Panel                   : ${kullanici.panelBasligi}');

  try {
    KullaniciFactory.fromMap({'id': 'u-7', 'ad': 'Deneme', 'rol': 'mudur'});
  } on ArgumentError catch (e) {
    print('Bilinmeyen rol yakalandı: ${e.message}');
  }

  // factory constructor: aynı değer için aynı nesne
  final a = Puan(0);
  final b = Puan(0);
  print(
    'Puan(0) identical mi?   : ${identical(a, b)}  (factory cache sayesinde true)',
  );
}

// ============================================================================
// BÖLÜM 2 — STRATEGY
//
// Problem: Karne notu birden fazla şekilde hesaplanabiliyor. Okul A düz
// ortalama istiyor, okul B son sınavı ağırlıklandırıyor, okul C en iyi N
// notu alıyor. Algoritma DEĞİŞKEN, çağıran kod SABİT kalmalı.
//
// Strategy = "yapılacak işi bir nesne hâline getir, arayüzün arkasına koy".
// ============================================================================

abstract interface class NotPolitikasi {
  String get ad;

  /// puanlar kronolojik sırada gelir (en yenisi sonda).
  double hesapla(List<int> puanlar);
}

class DuzOrtalama implements NotPolitikasi {
  const DuzOrtalama();

  @override
  String get ad => 'Düz ortalama';

  @override
  double hesapla(List<int> puanlar) =>
      puanlar.reduce((a, b) => a + b) / puanlar.length;
}

class SonSinaviAgirliklandir implements NotPolitikasi {
  final double sonSinavAgirligi; // 0.0 - 1.0

  const SonSinaviAgirliklandir({this.sonSinavAgirligi = 0.5});

  @override
  String get ad => 'Son sınav %${(sonSinavAgirligi * 100).round()} ağırlıklı';

  @override
  double hesapla(List<int> puanlar) {
    if (puanlar.length == 1) return puanlar.first.toDouble();
    final son = puanlar.last;
    final oncekiler = puanlar.sublist(0, puanlar.length - 1);
    final oncekiOrtalama = oncekiler.reduce((a, b) => a + b) / oncekiler.length;
    return son * sonSinavAgirligi + oncekiOrtalama * (1 - sonSinavAgirligi);
  }
}

class EnIyiNNot implements NotPolitikasi {
  final int n;

  const EnIyiNNot(this.n);

  @override
  String get ad => 'En iyi $n not';

  @override
  double hesapla(List<int> puanlar) {
    final kopya = [...puanlar]..sort((x, y) => y.compareTo(x));
    final secilen = kopya.take(n).toList();
    return secilen.reduce((a, b) => a + b) / secilen.length;
  }
}

/// Context sınıfı: politikayı BİLMEZ, sadece kullanır.
/// Yeni politika eklendiğinde bu sınıf hiç değişmez (Open/Closed — Gün 17).
class KarneHesaplayici {
  NotPolitikasi _politika;

  KarneHesaplayici(this._politika);

  set politika(NotPolitikasi yeni) => _politika = yeni;

  double ortalamaHesapla(List<int> puanlar) {
    if (puanlar.isEmpty) {
      throw StateError('Puan listesi boş; karne hesaplanamaz.');
    }
    return _politika.hesapla(puanlar);
  }

  String rapor(String ogrenciAdi, List<int> puanlar) {
    final sonuc = ortalamaHesapla(puanlar);
    return '$ogrenciAdi -> ${sonuc.toStringAsFixed(2)}  [${_politika.ad}]';
  }
}

void bolum2Strategy() {
  final puanlar = <int>[40, 65, 70, 95]; // öğrenci sonradan toparlamış
  final hesaplayici = KarneHesaplayici(const DuzOrtalama());

  print(hesaplayici.rapor('Mehmet', puanlar));

  // Aynı nesne, çalışma anında farklı algoritma:
  hesaplayici.politika = const SonSinaviAgirliklandir(sonSinavAgirligi: 0.6);
  print(hesaplayici.rapor('Mehmet', puanlar));

  hesaplayici.politika = const EnIyiNNot(2);
  print(hesaplayici.rapor('Mehmet', puanlar));

  print(
    '\nAynı veri, üç farklı sonuç — çağıran kodun tek satırı bile değişmedi.',
  );
}

// ============================================================================
// BÖLÜM 3 — REPLACE CONDITIONALS
//
// Bölüm 1'deki switch "hangi nesneyi kurayım?" idi (Factory'de kalır).
// Buradaki switch ise "hangi DAVRANIŞI çalıştırayım?" — asıl temizlenmesi
// gereken tür budur.
// ============================================================================

// ----------------------------------------------------------------------------
// ÖNCE: tek sınıf, üç işi de biliyor. SRP ihlali (Gün 4).
// Yeni kanal = bu metodu tekrar açmak, tekrar test etmek.
// ----------------------------------------------------------------------------

class BildirimServisiEski {
  void gonder(String kanal, Kullanici alici, String mesaj) {
    if (kanal == 'push') {
      print('  [FCM]  token alınıyor -> ${alici.ad}: $mesaj');
    } else if (kanal == 'sms') {
      final kisa = mesaj.length > 20 ? '${mesaj.substring(0, 20)}...' : mesaj;
      print('  [SMS]  operatöre gidiyor -> ${alici.ad}: $kisa');
    } else if (kanal == 'email') {
      print('  [MAIL] SMTP kuyruğu -> ${alici.ad}: $mesaj');
    } else {
      print('  [!] Bilinmeyen kanal: $kanal');
    }
  }
}

// ----------------------------------------------------------------------------
// SONRA: her dal kendi sınıfı. Her biri ayrı ayrı test edilebilir.
// ----------------------------------------------------------------------------

abstract interface class BildirimKanali {
  String get kod;
  void gonder(Kullanici alici, String mesaj);
}

class PushKanali implements BildirimKanali {
  @override
  String get kod => 'push';

  @override
  void gonder(Kullanici alici, String mesaj) =>
      print('  [FCM]  token alınıyor -> ${alici.ad}: $mesaj');
}

class SmsKanali implements BildirimKanali {
  final int karakterSiniri;

  const SmsKanali({this.karakterSiniri = 20});

  @override
  String get kod => 'sms';

  @override
  void gonder(Kullanici alici, String mesaj) {
    final kisa = mesaj.length > karakterSiniri
        ? '${mesaj.substring(0, karakterSiniri)}...'
        : mesaj;
    print('  [SMS]  operatöre gidiyor -> ${alici.ad}: $kisa');
  }
}

class EpostaKanali implements BildirimKanali {
  @override
  String get kod => 'email';

  @override
  void gonder(Kullanici alici, String mesaj) =>
      print('  [MAIL] SMTP kuyruğu -> ${alici.ad}: $mesaj');
}

/// Factory + Strategy birlikte: factory doğru strategy'yi seçer.
/// Kayıt tablosu sayesinde yeni kanal eklemek için BU SINIFI düzenlemek
/// gerekmez — dışarıdan kaydedebilirsin.
class KanalFactory {
  static final Map<String, BildirimKanali Function()> _kayit = {
    'push': () => PushKanali(),
    'sms': () => const SmsKanali(),
    'email': () => EpostaKanali(),
  };

  static void kaydet(String kod, BildirimKanali Function() uretici) {
    _kayit[kod.toLowerCase()] = uretici;
  }

  static BildirimKanali olustur(String kod) {
    final uretici = _kayit[kod.toLowerCase()];
    if (uretici == null) {
      throw ArgumentError('Bilinmeyen bildirim kanalı: "$kod"');
    }
    return uretici();
  }

  static List<String> get kayitliKodlar => _kayit.keys.toList();
}

class BildirimServisi {
  void gonder(String kanalKodu, Kullanici alici, String mesaj) {
    KanalFactory.olustur(kanalKodu).gonder(alici, mesaj);
  }

  /// Tek mesajı birden fazla kanaldan yollamak artık bedava geldi.
  void hepsindenGonder(List<String> kodlar, Kullanici alici, String mesaj) {
    for (final kod in kodlar) {
      gonder(kod, alici, mesaj);
    }
  }
}

// Sonradan eklenen kanal — mevcut hiçbir sınıfa dokunmadan.
class WhatsAppKanali implements BildirimKanali {
  @override
  String get kod => 'whatsapp';

  @override
  void gonder(Kullanici alici, String mesaj) =>
      print('  [WA]   business API -> ${alici.ad}: $mesaj');
}

void bolum3KosullariDegistir() {
  final veli = KullaniciFactory.fromMap({
    'id': 'u-42',
    'ad': 'Ayşe Yılmaz',
    'rol': 'veli',
    'cocuklar': ['u-90'],
  });

  print('ÖNCE (if-zinciri):');
  BildirimServisiEski().gonder('sms', veli, 'Ödeviniz teslim edilmedi.');

  print('\nSONRA (strategy + factory):');
  final servis = BildirimServisi();
  servis.hepsindenGonder(
    ['push', 'sms', 'email'],
    veli,
    'Ödeviniz teslim edilmedi.',
  );

  print('\nYeni kanal ekleniyor (mevcut kod hiç değişmiyor):');
  KanalFactory.kaydet('whatsapp', () => WhatsAppKanali());
  servis.gonder('whatsapp', veli, 'Ödeviniz teslim edilmedi.');
  print('Kayıtlı kanallar: ${KanalFactory.kayitliKodlar}');
}

// ============================================================================
// BÖLÜM 4 — DON'T PATTERN-HUNT
//
// Pattern bir maliyettir: dosya sayısı, dolaylılık, "bu çağrı nereye gidiyor?"
// sorusu. Değişkenlik yoksa bu maliyeti ödemenin karşılığı yoktur.
// ============================================================================

// ----------------------------------------------------------------------------
// KARŞI-ÖRNEK 1: Tek uygulaması olan bir iş için pattern kurmak.
//
// Aşağıdaki "TarihBicimlendirmeStratejisi" bir tek sınıf tarafından
// uygulanıyor ve ikinci bir uygulama gelmesi için hiçbir sebep yok.
// Arayüz + factory + context = üç kavram, sıfır kazanç.
// ----------------------------------------------------------------------------

// abstract interface class TarihBicimlendirmeStratejisi {
//   String bicimle(DateTime t);
// }
// class VarsayilanTarihBicimi implements TarihBicimlendirmeStratejisi { ... }
// class TarihBicimiFactory { static ... olustur() => VarsayilanTarihBicimi(); }

/// Yeterli olan: düz bir sınıf (hatta düz bir fonksiyon).
class TarihBicimleyici {
  const TarihBicimleyici();

  String gunAy(DateTime t) =>
      '${t.day.toString().padLeft(2, '0')}.${t.month.toString().padLeft(2, '0')}';
}

// ----------------------------------------------------------------------------
// KARŞI-ÖRNEK 2: Strategy'nin tek metodu ve durumu yoksa, sınıf yerine
// fonksiyon tipi yeterlidir. Dart'ta fonksiyonlar birinci sınıf vatandaş;
// Java'dan gelen "her şey sınıf olmalı" refleksini burada uygulama.
// ----------------------------------------------------------------------------

typedef GecikmeUcreti = int Function(int gecikenGun);

int sabitGunlukUcret(int gun) => gun * 5;
int artanUcret(int gun) => gun <= 3 ? gun * 5 : 15 + (gun - 3) * 10;

class OdevTakibi {
  final GecikmeUcreti _ucretPolitikasi;

  const OdevTakibi(this._ucretPolitikasi);

  int ceza(int gecikenGun) => _ucretPolitikasi(gecikenGun);
}

// ----------------------------------------------------------------------------
// KARŞI-ÖRNEK 3: İki dallı, büyümeyecek bir if.
// "Aktif mi, pasif mi" gibi ikili ve kapalı bir ayrım için strategy kurmak,
// okunabilirliği düşürür. if kalsın.
// ----------------------------------------------------------------------------

class Abonelik {
  final bool aktif;
  const Abonelik({required this.aktif});

  String durumEtiketi() => aktif ? 'Aktif' : 'Pasif'; // pattern gerekmez
}

void bolum4PatternAvlamaAma() {
  print(
    'Düz sınıf yeter        : ${const TarihBicimleyici().gunAy(DateTime(2026, 3, 7))}',
  );

  final sabit = const OdevTakibi(sabitGunlukUcret);
  final artan = const OdevTakibi(artanUcret);
  print(
    'Fonksiyon strategy (5g): sabit=${sabit.ceza(5)}  artan=${artan.ceza(5)}',
  );

  print(
    'İkili if yeter         : ${const Abonelik(aktif: false).durumEtiketi()}',
  );

  print('''
\nKARAR KURALI
  Pattern KUR   : aynı karar 2+ yerde tekrarlıyorsa,
                  yeni seçenek eklemek mevcut sınıfı açmayı gerektiriyorsa,
                  dalları ayrı ayrı test etmek istiyorsan.
  Pattern KURMA : tek uygulama varsa,
                  dallar ikili ve kapalıysa,
                  strategy'nin tek metodu ve durumu yoksa (fonksiyon yeter),
                  "ileride lazım olur" dışında bir gerekçen yoksa.''');
}
