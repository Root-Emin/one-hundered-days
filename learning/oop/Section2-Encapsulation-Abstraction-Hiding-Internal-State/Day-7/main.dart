// ============================================================================
// GÜN 7 — INVARIANT VE VALIDATION  (Dart)
//
// Çalıştırmak için:  dart run gun7_invariant_ve_validation.dart
// veya dartpad.dev'e yapıştır.
//
// BÖLÜM 1 -> Invariant listesi ve _checkInvariants()
// BÖLÜM 2 -> Her mutator invariantı korur
// BÖLÜM 3 -> Fail fast: önce doğrula, sonra değiştir
// BÖLÜM 4 -> Throw vs Result: hangi hata nasıl bildirilir
// BÖLÜM 5 -> Unit mentality: edge case testleri
// ============================================================================

// ############################################################################
//
//  BÖLÜM 1 — BU NESNENİN ÜÇ SÖZÜ (INVARIANTS)
//
//  I1: Bakiye ASLA negatif olamaz.
//      0 <= _balanceKurus
//
//  I2: Bakiye, işlem geçmişinin toplamına HER ZAMAN eşittir.
//      _balanceKurus == _transactions.map(amountKurus).sum
//      (Defter tutmalı. Bakiyeyi değiştirip kayıt düşmemek bu sözü bozar.)
//
//  I3: E-posta HER ZAMAN geçerli formatta olmalıdır.
//      _isValidEmail(_email) == true
//
//  Bu üç ifade, nesnenin hayatının HER ANINDA doğru olmalı — sadece
//  doğum anında değil. Bir metodun ortasında geçici olarak bozulabilirler,
//  ama metot bittiğinde yine doğru olmak zorundalar.
//
// ############################################################################

// ============================================================================
// HATA TİPLERİ
//
// Üç ayrı durum, üç ayrı tip. Aynı kefeye koymak çağıran tarafın işini
// zorlaştırır; hangi hatayı yakalayacağını bilemez.
//
//   ArgumentError -> Çağıran saçma bir GİRDİ verdi. (negatif tutar)
//                    Bu bir programlama hatası; kullanıcıya gösterilmez.
//
//   StateError    -> Girdi doğru ama nesnenin ŞU ANKİ DURUMU buna uygun
//                    değil. (kapalı hesaba para yatırma)
//
//   Özel exception-> Domain'in normal karşıladığı, BEKLENEN bir başarısızlık.
//                    (yetersiz bakiye) Ekstra bilgi taşıyabilir.
// ============================================================================

class InsufficientFundsException implements Exception {
  final int requestedKurus;
  final int availableKurus;

  const InsufficientFundsException({
    required this.requestedKurus,
    required this.availableKurus,
  });

  /// Ne kadar eksik kaldığını çağıran tarafa söyler. Genel bir Exception
  /// bunu yapamaz; özel tip yazmanın asıl sebebi bu.
  int get shortfallKurus => requestedKurus - availableKurus;
  double get shortfall => shortfallKurus / 100;

  @override
  String toString() =>
      'Yetersiz bakiye: ${shortfall.toStringAsFixed(2)} ₺ eksik';
}

// ============================================================================
// BÖLÜM 4 — RESULT TİPİ
//
// Bazen exception fırlatmak doğru değildir. "Yetersiz bakiye" bir hata
// değil, beklenen bir sonuçtur; kullanıcı ekranda görecek. Flutter'da
// bunu try/catch ile yönetmek yerine dönüş değeri yapmak daha temizdir.
//
// 'sealed' -> bu sınıfın alt tiplerinin tamamı bu dosyada. Derleyici
// switch'in bütün ihtimalleri kapsadığını kontrol edebiliyor.
// ============================================================================

sealed class Result<T> {
  const Result();
}

class Success<T> extends Result<T> {
  final T value;
  const Success(this.value);
}

class Failure<T> extends Result<T> {
  final String message;
  const Failure(this.message);
}

// ============================================================================
// İŞLEM KAYDI (immutable)
// ============================================================================

class Transaction {
  final String type;
  final int amountKurus; // işaretli: yatırma +, çekme -
  final DateTime at;

  const Transaction({
    required this.type,
    required this.amountKurus,
    required this.at,
  });

  double get amount => amountKurus / 100;

  @override
  String toString() =>
      '${type.padRight(12)} ${amount >= 0 ? '+' : ''}${amount.toStringAsFixed(2)} ₺';
}

// ============================================================================
// BANKA HESABI
// ============================================================================

class BankAccount {
  final String iban;
  final String ownerName;

  String _email;
  int _balanceKurus;
  bool _isClosed = false;
  final List<Transaction> _transactions = [];

  /// Tek seferde çekilebilecek üst sınır. Kural sabit olarak duruyor;
  /// koda gömülü sihirli sayı değil.
  static const int maxSingleWithdrawalKurus = 5000000; // 50.000,00 ₺

  BankAccount({
    required this.iban,
    required this.ownerName,
    required String email,
    double openingBalance = 0,
  }) : _email = email.trim().toLowerCase(),
       _balanceKurus = 0 {
    // ---- Doğum anı doğrulaması (Gün 3) ----
    if (iban.trim().length < 10) {
      throw ArgumentError.value(iban, 'iban', 'IBAN en az 10 karakter olmalı');
    }
    if (ownerName.trim().isEmpty) {
      throw ArgumentError.value(ownerName, 'ownerName', 'İsim boş olamaz');
    }
    if (!_isValidEmail(_email)) {
      throw ArgumentError.value(email, 'email', 'Geçersiz e-posta formatı');
    }
    if (openingBalance < 0) {
      throw ArgumentError.value(
        openingBalance,
        'openingBalance',
        'Negatif olamaz',
      );
    }

    // ------------------------------------------------------------------
    // I2 TASARIMI ŞEKİLLENDİRİYOR
    //
    // "Bakiye = işlemler toplamı" sözünü verdiysek, açılış bakiyesi de
    // bir işlem olmak zorunda. Yoksa nesne DOĞDUĞU AN sözünü bozar.
    //
    // Invariant yazmanın en büyük faydası bu: tasarımdaki boşlukları
    // kod yazmadan önce gösteriyor.
    // ------------------------------------------------------------------
    final openingKurus = (openingBalance * 100).round();
    if (openingKurus > 0) {
      _balanceKurus = openingKurus;
      _transactions.add(
        Transaction(
          type: 'Açılış',
          amountKurus: openingKurus,
          at: DateTime.now(),
        ),
      );
    }

    _checkInvariants();
  }

  // ==========================================================================
  // INVARIANT KONTROLÜ
  //
  // Durumu değiştiren HER metodun sonunda çağrılır. Burada fırlayan hata
  // kullanıcının hatası değil, BİZİM hatamızdır: "bu duruma hiç
  // düşmemeliydik" demektir. O yüzden mesajlar geliştiriciye yazılmış.
  //
  // Bu tarz bir kontrolü tek yerde toplamak, yeni bir mutator eklerken
  // korumayı unutma ihtimalini azaltır.
  // ==========================================================================
  void _checkInvariants() {
    // I1
    if (_balanceKurus < 0) {
      throw StateError(
        'INVARIANT #1 İHLALİ: bakiye negatif ($_balanceKurus kuruş)',
      );
    }

    // I2
    final ledgerSum = _transactions.fold<int>(
      0,
      (sum, t) => sum + t.amountKurus,
    );
    if (ledgerSum != _balanceKurus) {
      throw StateError(
        'INVARIANT #2 İHLALİ: defter tutmuyor (bakiye $_balanceKurus, toplam $ledgerSum)',
      );
    }

    // I3
    if (!_isValidEmail(_email)) {
      throw StateError('INVARIANT #3 İHLALİ: geçersiz e-posta ($_email)');
    }
  }

  /// Kasıtlı olarak basit. Gerçek e-posta doğrulaması regex'le yapılamaz;
  /// RFC 5322 çok karmaşıktır. Pratikte kabul edilen yaklaşım: kabaca
  /// kontrol et, kesin doğrulamayı doğrulama e-postasıyla yap.
  static bool _isValidEmail(String value) {
    if (value.isEmpty || value.length > 254) return false;
    if (value.contains(' ')) return false;

    final parts = value.split('@');
    if (parts.length != 2) return false; // '@' yok veya birden fazla

    final local = parts[0];
    final domain = parts[1];

    if (local.isEmpty || domain.isEmpty) return false;
    if (!domain.contains('.')) return false;
    if (domain.startsWith('.') || domain.endsWith('.')) return false;
    if (domain.contains('..')) return false;

    return true;
  }

  /// Tutar dönüşümünün TEK yeri. Üç edge case'i birlikte yakalıyor:
  /// NaN/Infinity, sıfır veya negatif, ve kuruşun altında kalan tutarlar.
  static int _toKurus(double amount, String paramName) {
    if (amount.isNaN || amount.isInfinite) {
      throw ArgumentError.value(amount, paramName, 'Geçerli bir sayı olmalı');
    }
    final kurus = (amount * 100).round();
    if (kurus <= 0) {
      throw ArgumentError.value(amount, paramName, 'Tutar en az 0,01 ₺ olmalı');
    }
    return kurus;
  }

  void _ensureOpen() {
    if (_isClosed) {
      throw StateError('Hesap kapalı ($iban); işlem yapılamaz');
    }
  }

  // ---- QUERY'LER ----
  double get balance => _balanceKurus / 100;
  bool get isClosed => _isClosed;
  String get email => _email;
  int get transactionCount => _transactions.length;
  List<Transaction> get transactions => List.unmodifiable(_transactions);

  bool canWithdraw(double amount) {
    if (_isClosed) return false;
    if (amount.isNaN || amount.isInfinite) return false;
    final kurus = (amount * 100).round();
    return kurus > 0 &&
        kurus <= _balanceKurus &&
        kurus <= maxSingleWithdrawalKurus;
  }

  // ==========================================================================
  // BÖLÜM 2 + 3 — MUTATOR'LAR
  //
  // Hepsi aynı iskeleti izliyor:
  //   1) Durumu kontrol et      (_ensureOpen)
  //   2) Girdiyi doğrula        (_toKurus, limit kontrolleri)
  //   3) İşlemin mümkün olduğunu doğrula (yeterli bakiye var mı)
  //   4) ANCAK ŞİMDİ durumu değiştir
  //   5) Invariantları kontrol et
  //
  // 2 ve 3'ün 4'ten ÖNCE gelmesi kritik. Sırası ters olsaydı, hata
  // fırladığında nesne yarım değişmiş halde kalırdı.
  // ==========================================================================

  void deposit(double amount) {
    _ensureOpen(); // 1
    final kurus = _toKurus(amount, 'amount'); // 2

    _balanceKurus += kurus; // 4
    _transactions.add(
      Transaction(type: 'Yatırma', amountKurus: kurus, at: DateTime.now()),
    );

    _checkInvariants(); // 5
  }

  void withdraw(double amount) {
    _ensureOpen(); // 1
    final kurus = _toKurus(amount, 'amount'); // 2

    if (kurus > maxSingleWithdrawalKurus) {
      // 3
      throw ArgumentError.value(
        amount,
        'amount',
        'Tek seferde en fazla ${maxSingleWithdrawalKurus / 100} ₺ çekilebilir',
      );
    }
    if (kurus > _balanceKurus) {
      // I1'i koruyan asıl satır. Bu kontrol olmasa bakiye negatife düşerdi.
      throw InsufficientFundsException(
        requestedKurus: kurus,
        availableKurus: _balanceKurus,
      );
    }

    _balanceKurus -= kurus; // 4
    _transactions.add(
      Transaction(type: 'Çekme', amountKurus: -kurus, at: DateTime.now()),
    );

    _checkInvariants(); // 5
  }

  void changeEmail(String newEmail) {
    _ensureOpen();
    final normalized = newEmail.trim().toLowerCase();

    // I3'ü koruyan satır. Doğrulama ATAMADAN ÖNCE.
    if (!_isValidEmail(normalized)) {
      throw ArgumentError.value(
        newEmail,
        'newEmail',
        'Geçersiz e-posta formatı',
      );
    }

    _email = normalized;
    _checkInvariants();
  }

  /// İki nesneyi birden ilgilendiren işlem. Her ikisinin de invariantı
  /// korunmalı ve işlem YARIM KALMAMALI.
  ///
  /// Önce bütün kontroller, sonra iki değişiklik peş peşe. Böylece
  /// "para çekildi ama yatmadı" durumu oluşamıyor.
  void transferTo(BankAccount target, double amount) {
    _ensureOpen();
    target._ensureOpen();

    if (identical(this, target)) {
      throw ArgumentError('Hesap kendine transfer yapamaz');
    }

    final kurus = _toKurus(amount, 'amount');

    if (kurus > _balanceKurus) {
      throw InsufficientFundsException(
        requestedKurus: kurus,
        availableKurus: _balanceKurus,
      );
    }

    // Buradan sonrası artık başarısız olamaz.
    withdraw(amount);
    target.deposit(amount);
  }

  void close() {
    if (_isClosed) {
      throw StateError('Hesap zaten kapalı');
    }
    if (_balanceKurus != 0) {
      throw StateError(
        'Bakiyesi olan hesap kapatılamaz (${balance.toStringAsFixed(2)} ₺)',
      );
    }

    _isClosed = true;
    _checkInvariants();
  }

  // ==========================================================================
  // BÖLÜM 4 — AYNI İŞ, THROW YERİNE RESULT
  //
  // "Yetersiz bakiye" kullanıcının göreceği normal bir sonuç. Bunu
  // exception yapmak, çağıran tarafı her yerde try/catch yazmaya zorlar.
  //
  // Kural: BEKLENEN başarısızlıklar dönüş değeri olur,
  //        BEKLENMEYEN durumlar exception olur.
  // ==========================================================================
  Result<double> tryWithdraw(double amount) {
    if (_isClosed) return const Failure('Hesap kapalı');
    if (amount.isNaN || amount.isInfinite)
      return const Failure('Geçersiz tutar');

    final kurus = (amount * 100).round();
    if (kurus <= 0) return const Failure('Tutar en az 0,01 ₺ olmalı');
    if (kurus > maxSingleWithdrawalKurus) return const Failure('Limit aşıldı');

    if (kurus > _balanceKurus) {
      final eksik = (kurus - _balanceKurus) / 100;
      return Failure('${eksik.toStringAsFixed(2)} ₺ eksiğiniz var');
    }

    withdraw(amount);
    return Success(balance);
  }

  // ==========================================================================
  // KÖTÜ ÖRNEK — ÖNCE DEĞİŞTİR, SONRA DOĞRULA
  //
  // Bu metot invariantı KONTROL EDİYOR ama iş işten geçtikten sonra.
  // Hata fırlıyor, evet — ama nesne çoktan bozulmuş durumda kalıyor.
  // Bölüm 3 testinde bunun sonucunu göreceksin.
  // ==========================================================================
  void unsafeWithdraw(double amount) {
    final kurus = (amount * 100).round();

    _balanceKurus -= kurus; // ÖNCE değiştirdik
    _transactions.add(
      Transaction(type: 'Çekme(!)', amountKurus: -kurus, at: DateTime.now()),
    );

    _checkInvariants(); // SONRA kontrol ettik — çok geç
  }

  @override
  String toString() =>
      '$ownerName ($iban) | ${balance.toStringAsFixed(2)} ₺'
      '${_isClosed ? ' | KAPALI' : ''}';
}

// ############################################################################
//
//  BÖLÜM 5 — MİNİ TEST ALTYAPISI
//
//  Gerçek projede 'package:test' kullanırsın. Burada bağımlılık olmasın
//  diye 20 satırlık kendi harness'ımızı yazıyoruz. Fikir aynı:
//  bir iddiada bulun, tutup tutmadığını otomatik kontrol et.
//
// ############################################################################

int _passed = 0;
int _failed = 0;

void check(String label, bool condition) {
  if (condition) {
    _passed++;
    print('  ✓ $label');
  } else {
    _failed++;
    print('  ✗ $label  <-- BAŞARISIZ');
  }
}

/// Belirli bir tipte hata fırlamasını bekler.
void expectThrows<T>(String label, void Function() action) {
  try {
    action();
    _failed++;
    print('  ✗ $label  <-- hata bekleniyordu, fırlamadı');
  } catch (e) {
    if (e is T) {
      _passed++;
      print('  ✓ $label');
    } else {
      _failed++;
      print('  ✗ $label  <-- $T bekleniyordu, ${e.runtimeType} geldi');
    }
  }
}

void expectNoThrow(String label, void Function() action) {
  try {
    action();
    _passed++;
    print('  ✓ $label');
  } catch (e) {
    _failed++;
    print('  ✗ $label  <-- beklenmeyen hata: $e');
  }
}

BankAccount _hesapUret({double bakiye = 100}) => BankAccount(
  iban: 'TR000000000001',
  ownerName: 'Test Kullanıcı',
  email: 'test@example.com',
  openingBalance: bakiye,
);

// ============================================================================
// ÇALIŞTIRMA
// ============================================================================

void main() {
  print('=== BÖLÜM 1: NORMAL KULLANIM ===');

  final hesap = BankAccount(
    iban: 'TR330006100519786457841326',
    ownerName: 'Ayşe Kaya',
    email: 'Ayse.Kaya@Example.COM ', // boşluk ve büyük harf -> normalize
    openingBalance: 1500,
  );

  print(hesap);
  print('E-posta normalize edildi: ${hesap.email}');

  hesap.deposit(250.50);
  hesap.withdraw(100);
  print('Son bakiye: ${hesap.balance.toStringAsFixed(2)} ₺');
  print('İşlem geçmişi:');
  for (final t in hesap.transactions) {
    print('    $t');
  }
  print('');

  print('=== BÖLÜM 3: FAIL FAST — SIRA ÖNEMLİ ===');

  final guvenli = _hesapUret(bakiye: 100);
  try {
    guvenli.withdraw(500); // bakiyeden fazla
  } on InsufficientFundsException catch (e) {
    print('Güvenli metot reddetti: $e');
  }
  print('  Hata sonrası bakiye: ${guvenli.balance} ₺  <- bozulmadı');
  print('  İşlem sayısı: ${guvenli.transactionCount}  <- sahte kayıt yok');

  final bozuk = _hesapUret(bakiye: 100);
  try {
    bozuk.unsafeWithdraw(500); // önce değiştir, sonra kontrol et
  } on StateError catch (e) {
    print('Kötü metot da hata verdi: ${e.message}');
  }
  print('  Hata sonrası bakiye: ${bozuk.balance} ₺  <- NEGATİF, nesne bozuk');
  print('  İşlem sayısı: ${bozuk.transactionCount}  <- sahte kayıt kaldı');
  print('  Ders: hata fırlatmak yetmez, DEĞİŞTİRMEDEN ÖNCE fırlatmalı.');
  print('');

  print('=== BÖLÜM 4: THROW vs RESULT ===');

  final r1 = hesap.tryWithdraw(50);
  switch (r1) {
    case Success(:final value):
      print('Başarılı, yeni bakiye: ${value.toStringAsFixed(2)} ₺');
    case Failure(:final message):
      print('Başarısız: $message');
  }

  final r2 = hesap.tryWithdraw(999999);
  switch (r2) {
    case Success(:final value):
      print('Başarılı, yeni bakiye: ${value.toStringAsFixed(2)} ₺');
    case Failure(:final message):
      print('Başarısız: $message  <- try/catch yok, sadece bir if');
  }
  print('');

  print('=== BÖLÜM 5: EDGE CASE TESTLERİ ===');

  print('--- I1: Bakiye asla negatif olamaz ---');
  {
    final h = _hesapUret(bakiye: 100);
    expectNoThrow(
      'Tam bakiye kadar çekilebilir (100.00)',
      () => h.withdraw(100),
    );
    check('Bakiye tam sıfır', h.balance == 0);

    final h2 = _hesapUret(bakiye: 100);
    expectThrows<InsufficientFundsException>(
      'Bir kuruş fazlası reddedilir (100.01)',
      () => h2.withdraw(100.01),
    );
    check('Reddedilince bakiye değişmedi', h2.balance == 100);

    expectThrows<ArgumentError>('Sıfır çekilemez', () => h2.withdraw(0));
    expectThrows<ArgumentError>('Negatif çekilemez', () => h2.withdraw(-50));
    expectThrows<ArgumentError>(
      'Kuruş altı tutar reddedilir (0.004)',
      () => h2.withdraw(0.004),
    );
    expectThrows<ArgumentError>(
      'NaN reddedilir',
      () => h2.withdraw(double.nan),
    );
    expectThrows<ArgumentError>(
      'Infinity reddedilir',
      () => h2.withdraw(double.infinity),
    );
    expectThrows<ArgumentError>(
      'Tek işlem limiti aşılamaz',
      () => h2.withdraw(60000),
    );
  }

  print('--- I2: Bakiye = işlem toplamı ---');
  {
    final h = _hesapUret(bakiye: 500);
    h.deposit(123.45);
    h.withdraw(78.90);
    h.deposit(0.01); // en küçük geçerli tutar

    final toplam = h.transactions.fold<int>(0, (s, t) => s + t.amountKurus);
    check('Defter tutuyor', toplam == (h.balance * 100).round());
    check(
      'Açılış da işlem olarak kayıtlı',
      h.transactions.first.type == 'Açılış',
    );

    final bosHesap = BankAccount(
      iban: 'TR000000000002',
      ownerName: 'Sıfır Bakiye',
      email: 'a@b.co',
    );
    check('Sıfır açılışta kayıt oluşmaz', bosHesap.transactionCount == 0);
    check('Sıfır açılışta defter yine tutuyor', bosHesap.balance == 0);
  }

  print('--- I3: E-posta her zaman geçerli ---');
  {
    final h = _hesapUret();

    expectNoThrow('Normal e-posta', () => h.changeEmail('yeni@site.com'));
    check(
      'Küçük harfe çevrildi',
      (h..changeEmail('BUYUK@SITE.COM')).email == 'buyuk@site.com',
    );

    const gecersizler = [
      '', // boş
      'nokta yok@x', // domain'de nokta yok
      'atsiz.com', // @ yok
      'a@@b.com', // çift @
      '@site.com', // local kısım boş
      'user@', // domain boş
      'a b@site.com', // boşluk
      'a@site..com', // arka arkaya nokta
      'a@.com', // nokta ile başlıyor
      'a@site.', // nokta ile bitiyor
    ];
    for (final g in gecersizler) {
      expectThrows<ArgumentError>('Reddedilir: "$g"', () => h.changeEmail(g));
    }
    check(
      'Reddedilenlerden sonra e-posta hâlâ geçerli',
      h.email == 'buyuk@site.com',
    );
  }

  print('--- Durum kuralları ---');
  {
    final h = _hesapUret(bakiye: 50);
    expectThrows<StateError>('Bakiyeli hesap kapatılamaz', () => h.close());

    h.withdraw(50);
    expectNoThrow('Sıfır bakiyeli hesap kapatılır', () => h.close());
    check('Hesap kapalı', h.isClosed);

    expectThrows<StateError>('Kapalı hesaba yatırılamaz', () => h.deposit(10));
    expectThrows<StateError>('Kapalı hesaptan çekilemez', () => h.withdraw(10));
    expectThrows<StateError>(
      'Kapalı hesabın e-postası değişmez',
      () => h.changeEmail('x@y.com'),
    );
    expectThrows<StateError>('İki kez kapatılamaz', () => h.close());
  }

  print('--- Transfer: iki nesnenin invariantı birlikte ---');
  {
    final a = _hesapUret(bakiye: 300);
    final b = BankAccount(
      iban: 'TR000000000003',
      ownerName: 'Alıcı',
      email: 'alici@site.com',
      openingBalance: 100,
    );

    final toplamOnce = a.balance + b.balance;
    a.transferTo(b, 120);

    check('Gönderenin bakiyesi düştü', a.balance == 180);
    check('Alıcının bakiyesi arttı', b.balance == 220);
    check('Toplam para korundu', a.balance + b.balance == toplamOnce);

    expectThrows<InsufficientFundsException>(
      'Yetersiz bakiyeyle transfer reddedilir',
      () => a.transferTo(b, 5000),
    );
    check('Reddedilen transfer sonrası gönderen bozulmadı', a.balance == 180);
    check('Reddedilen transfer sonrası alıcı bozulmadı', b.balance == 220);

    expectThrows<ArgumentError>(
      'Kendine transfer reddedilir',
      () => a.transferTo(a, 10),
    );
  }

  print('');
  print('=== SONUÇ ===');
  print('  Geçen: $_passed');
  print('  Kalan: $_failed');
  print(
    _failed == 0
        ? '  Tüm invariantlar korundu.'
        : '  DİKKAT: $_failed test başarısız!',
  );
}
