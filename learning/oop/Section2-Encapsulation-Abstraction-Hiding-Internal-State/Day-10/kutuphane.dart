// ============================================================================
// GÜN 10 — SERTLEŞTİRME (HARDENING)  (1/2: KÜTÜPHANE DOSYASI)
//
// Çalıştırmak için:  dart run gun10_main.dart
//
// Gün 5'teki kütüphane modelini alıp Gün 6-9'da öğrendiklerimizi
// uyguluyoruz. Domain aynı; tasarım sertleşiyor.
//
// GÜN 5'TEKİ ZAYIF NOKTALAR
//   1. isbn, id gibi kimlikler düz String'di. Yanlış String vermek
//      ya da parametreleri karıştırmak serbestti.
//   2. Alanların çoğu public final'dı; okunması serbest, anlamı belirsizdi.
//   3. Ceza düz double'dı. Para birimi ve yuvarlama belirsizdi.
//   4. Ödünç süresi düz int gündü. 5000 gün istemek mümkündü.
//   5. İade durumu bool'du. "returned=false + returnedAt dolu" gibi
//      anlamsız kombinasyonlar temsil edilebiliyordu.
//   6. Metot adları teknikti (create, markReturned).
//
// ============================================================================

import 'dart:collection';

// ############################################################################
//  DEĞER NESNELERİ (Gün 9)
//
//  "Primitive obsession" denen soruna karşı ilk savunma hattı.
//  Düz String yerine Isbn, düz double yerine Money kullanınca yanlış
//  değeri yanlış yere vermek DERLENMİYOR.
// ############################################################################

class Isbn {
  final String digits;

  // Private constructor: doğrulanmamış Isbn üretmenin yolu yok.
  const Isbn._(this.digits);

  factory Isbn.parse(String raw) {
    final cleaned = raw.replaceAll(RegExp(r'[\s-]'), '');

    if (cleaned.length != 13) {
      throw FormatException('ISBN 13 haneli olmalı, gelen: ${cleaned.length}');
    }
    if (int.tryParse(cleaned) == null) {
      throw FormatException('ISBN sadece rakam içermeli: $raw');
    }
    return Isbn._(cleaned);
  }

  @override
  bool operator ==(Object other) =>
      identical(this, other) || (other is Isbn && other.digits == digits);

  @override
  int get hashCode => digits.hashCode;

  @override
  String toString() =>
      '${digits.substring(0, 3)}-${digits.substring(3, 4)}-'
      '${digits.substring(4, 8)}-${digits.substring(8, 12)}-${digits.substring(12)}';
}

class MemberId {
  final String value;

  const MemberId._(this.value);

  factory MemberId.parse(String raw) {
    final cleaned = raw.trim().toUpperCase();
    if (!RegExp(r'^M-\d{4}$').hasMatch(cleaned)) {
      throw FormatException('Üye no M-1234 biçiminde olmalı, gelen: $raw');
    }
    return MemberId._(cleaned);
  }

  @override
  bool operator ==(Object other) =>
      identical(this, other) || (other is MemberId && other.value == value);

  @override
  int get hashCode => value.hashCode;

  @override
  String toString() => value;
}

class Money {
  final int kurus;

  const Money(this.kurus);
  static const zero = Money(0);

  double get lira => kurus / 100;
  bool get isZero => kurus == 0;

  Money operator +(Money other) => Money(kurus + other.kurus);
  Money operator *(int factor) => Money(kurus * factor);
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
//  ENUM'LAR — GEÇERSİZ DURUMLARI TEMSİL EDİLEMEZ KILMAK
//
//  Gün 5'te süre düz int'ti: lendTo(member, days: 5000) mümkündü.
//  Şimdi sadece kütüphanenin gerçekten sunduğu iki süre var; üçüncüsünü
//  yazmak DERLENMİYOR. Doğrulama koduna gerek kalmadı, çünkü geçersiz
//  değer ifade edilemiyor.
// ############################################################################

enum LoanPeriod {
  twoWeeks(14, 'iki hafta'),
  oneMonth(30, 'bir ay');

  const LoanPeriod(this.days, this.label);

  final int days;
  final String label;
}

/// Gün 5'te iki ayrı bool vardı (returned, lost). Dört kombinasyon
/// mümkündü ama sadece üçü anlamlıydı. Enum ile anlamsız kombinasyon
/// yok: bir ödünç aynı anda hem iade edilmiş hem kayıp olamaz.
enum LoanState { active, returned, lost }

// ############################################################################
//  ÜYE
// ############################################################################

class Member {
  // ---- BÜTÜN ALANLAR PRIVATE (Gün 6) ----
  final MemberId _id;
  String _fullName;
  final int _borrowingLimit;
  final List<Loan> _openLoans = [];
  Money _paidFines = Money.zero;

  static const Money dailyOverdueFine = Money(250); // 2,50 ₺

  Member._({
    required MemberId id,
    required String fullName,
    required int borrowingLimit,
  }) : _id = id,
       _fullName = fullName,
       _borrowingLimit = borrowingLimit;

  /// DOMAIN LANGUAGE: kütüphaneciler "üye kaydı açmak" der,
  /// "Member nesnesi oluşturmak" demez.
  factory Member.register({
    required String memberId,
    required String fullName,
    int borrowingLimit = 2,
  }) {
    final trimmedName = fullName.trim();

    if (trimmedName.length < 2) {
      throw ArgumentError.value(fullName, 'fullName', 'İsim en az 2 karakter');
    }
    if (borrowingLimit < 1 || borrowingLimit > 10) {
      throw ArgumentError.value(
        borrowingLimit,
        'borrowingLimit',
        '1 ile 10 arasında olmalı',
      );
    }

    final member = Member._(
      id: MemberId.parse(memberId), // geçersizse burada patlar
      fullName: trimmedName,
      borrowingLimit: borrowingLimit,
    );
    member._checkInvariants();
    return member;
  }

  // ---- SALT OKUNUR ERİŞİM ----
  MemberId get id => _id;
  String get fullName => _fullName;
  int get borrowingLimit => _borrowingLimit;
  int get openLoanCount => _openLoans.length;
  Money get paidFines => _paidFines;

  UnmodifiableListView<Loan> get openLoans => UnmodifiableListView(_openLoans);

  bool get hasOverdueItems => _openLoans.any((loan) => loan.isOverdue);

  Money get outstandingFines =>
      _openLoans.fold(Money.zero, (sum, loan) => sum + loan.accruedFine);

  /// DOMAIN LANGUAGE: kütüphanecilikte üyenin durumuna "standing" denir.
  /// canBorrow tek başına "neden hayır" sorusunu cevaplayamıyordu.
  String get standing {
    if (hasOverdueItems) return 'Gecikmiş kitabı var';
    if (_openLoans.length >= _borrowingLimit) {
      return 'Ödünç limiti dolu ($_borrowingLimit)';
    }
    return 'Uygun (${_borrowingLimit - _openLoans.length} hak kaldı)';
  }

  bool get canBorrow => !hasOverdueItems && _openLoans.length < _borrowingLimit;

  // ---- KONTROLLÜ GÜNCELLEME ----
  void rename(String newName) {
    final trimmed = newName.trim();
    if (trimmed.length < 2) {
      throw ArgumentError.value(newName, 'newName', 'İsim en az 2 karakter');
    }
    _fullName = trimmed;
    _checkInvariants();
  }

  // ---- Sadece bu kütüphanenin çağırabileceği işlemler ----
  void _attachLoan(Loan loan) {
    _openLoans.add(loan);
    _checkInvariants();
  }

  void _detachLoan(Loan loan, Money fine) {
    _openLoans.remove(loan);
    _paidFines = _paidFines + fine;
    _checkInvariants();
  }

  /// INVARIANT SUITE
  ///   M1: açık ödünç sayısı limiti aşamaz
  ///   M2: açık ödünçlerin hepsi gerçekten 'active' durumda olmalı
  ///   M3: isim hiçbir zaman boş olamaz
  void _checkInvariants() {
    if (_openLoans.length > _borrowingLimit) {
      throw StateError('İHLAL M1: ${_openLoans.length} > $_borrowingLimit');
    }
    if (_openLoans.any((l) => l.state != LoanState.active)) {
      throw StateError('İHLAL M2: kapanmış ödünç açık listede duruyor');
    }
    if (_fullName.trim().length < 2) {
      throw StateError('İHLAL M3: geçersiz isim');
    }
  }

  @override
  bool operator ==(Object other) =>
      identical(this, other) || (other is Member && other._id == _id);

  @override
  int get hashCode => _id.hashCode;

  @override
  String toString() => '$_fullName ($_id)';
}

// ############################################################################
//  KİTAP
// ############################################################################

class Book {
  final Isbn _isbn;
  final String _title;
  final String _author;
  final int _totalCopies;
  int _copiesOnShelf;

  Book._({
    required Isbn isbn,
    required String title,
    required String author,
    required int totalCopies,
  }) : _isbn = isbn,
       _title = title,
       _author = author,
       _totalCopies = totalCopies,
       _copiesOnShelf = totalCopies;

  /// DOMAIN LANGUAGE: kütüphaneye kitap eklemeye "kataloglama" denir.
  factory Book.catalog({
    required String isbn,
    required String title,
    required String author,
    required int copies,
  }) {
    if (title.trim().isEmpty) {
      throw ArgumentError.value(title, 'title', 'Başlık boş olamaz');
    }
    if (author.trim().isEmpty) {
      throw ArgumentError.value(author, 'author', 'Yazar boş olamaz');
    }
    if (copies < 1 || copies > 1000) {
      throw ArgumentError.value(copies, 'copies', '1 ile 1000 arasında olmalı');
    }

    final book = Book._(
      isbn: Isbn.parse(isbn),
      title: title.trim(),
      author: author.trim(),
      totalCopies: copies,
    );
    book._checkInvariants();
    return book;
  }

  // ---- SALT OKUNUR ERİŞİM ----
  Isbn get isbn => _isbn;
  String get title => _title;
  String get author => _author;
  int get totalCopies => _totalCopies;
  int get copiesOnShelf => _copiesOnShelf;
  int get copiesOnLoan => _totalCopies - _copiesOnShelf;
  bool get isOnShelf => _copiesOnShelf > 0;

  // ==========================================================================
  // DOMAIN LANGUAGE OPERASYONU
  //
  // Gün 5:  Loan.create(book: suc, member: ayse, days: 14)
  // Gün 10: suc.lendTo(ayse, period: LoanPeriod.twoWeeks)
  //
  // İkincisi kütüphanecinin cümlesine benziyor: "Suç ve Ceza'yı Ayşe'ye
  // iki haftalığına ödünç ver."
  // ==========================================================================
  Loan lendTo(
    Member member, {
    LoanPeriod period = LoanPeriod.twoWeeks,
    DateTime? on,
  }) {
    // FAIL FAST (Gün 7): önce bütün kontroller, sonra değişiklik.
    if (!isOnShelf) {
      throw StateError('"$_title" rafta yok');
    }
    if (!member.canBorrow) {
      throw StateError('${member.fullName} ödünç alamaz — ${member.standing}');
    }

    final borrowedOn = on ?? DateTime.now();
    final loan = Loan._(
      book: this,
      member: member,
      borrowedOn: borrowedOn,
      dueOn: borrowedOn.add(Duration(days: period.days)),
    );

    // Artık başarısız olamaz: iki nesne birlikte güncelleniyor.
    _copiesOnShelf--;
    member._attachLoan(loan);

    _checkInvariants();
    return loan;
  }

  void _placeBackOnShelf() {
    _copiesOnShelf++;
    _checkInvariants();
  }

  void _writeOffCopy() {
    // Kayıp kitap: kopya toplamdan düşmüyor ama rafa da dönmüyor.
    _checkInvariants();
  }

  /// INVARIANT SUITE
  ///   B1: raftaki kopya sayısı negatif olamaz
  ///   B2: raftaki kopya sayısı toplamı aşamaz
  void _checkInvariants() {
    if (_copiesOnShelf < 0) {
      throw StateError('İHLAL B1: "$_title" rafta negatif kopya');
    }
    if (_copiesOnShelf > _totalCopies) {
      throw StateError('İHLAL B2: "$_title" raf sayısı toplamı aşıyor');
    }
  }

  @override
  bool operator ==(Object other) =>
      identical(this, other) || (other is Book && other._isbn == _isbn);

  @override
  int get hashCode => _isbn.hashCode;

  @override
  String toString() =>
      '"$_title" — $_author | rafta $_copiesOnShelf/$_totalCopies';
}

// ############################################################################
//  ÖDÜNÇ
// ############################################################################

class Loan {
  static const int maxRenewals = 2;

  final Book _book;
  final Member _member;
  final DateTime _borrowedOn;
  DateTime _dueOn;
  DateTime? _closedOn;
  LoanState _state = LoanState.active;
  int _renewalCount = 0;

  // Private constructor: Loan üretmenin TEK yolu book.lendTo().
  // Bu sayede "kitap stoğu düşmeden ödünç oluşturmak" imkânsız.
  Loan._({
    required Book book,
    required Member member,
    required DateTime borrowedOn,
    required DateTime dueOn,
  }) : _book = book,
       _member = member,
       _borrowedOn = borrowedOn,
       _dueOn = dueOn;

  // ---- SALT OKUNUR ERİŞİM ----
  Book get book => _book;
  Member get member => _member;
  DateTime get borrowedOn => _borrowedOn;
  DateTime get dueOn => _dueOn;
  LoanState get state => _state;
  int get renewalsLeft => maxRenewals - _renewalCount;
  bool get isClosed => _state != LoanState.active;

  int get daysOverdue {
    final reference = _closedOn ?? DateTime.now();
    if (!reference.isAfter(_dueOn)) return 0;
    return reference.difference(_dueOn).inDays;
  }

  bool get isOverdue => _state == LoanState.active && daysOverdue > 0;

  Money get accruedFine => Member.dailyOverdueFine * daysOverdue;

  int get daysRemaining => _dueOn.difference(DateTime.now()).inDays;

  // ==========================================================================
  // DOMAIN LANGUAGE OPERASYONLARI
  //
  // Gün 5:  loan.extend(days: 7)   /  loan.markReturned()
  // Gün 10: loan.renew()           /  loan.returnToShelf()
  //
  // "renew" ve "return" kütüphanecinin kendi kelimeleri.
  // ==========================================================================

  void renew({LoanPeriod by = LoanPeriod.twoWeeks}) {
    if (isClosed) {
      throw StateError('Kapanmış ödünç uzatılamaz (durum: ${_state.name})');
    }
    if (isOverdue) {
      throw StateError('Süresi geçmiş ödünç uzatılamaz — önce iade edin');
    }
    if (_renewalCount >= maxRenewals) {
      throw StateError('En fazla $maxRenewals kez uzatılabilir');
    }

    _dueOn = _dueOn.add(Duration(days: by.days));
    _renewalCount++;
    _checkInvariants();
  }

  /// İade eder ve varsa gecikme cezasını döndürür.
  Money returnToShelf({DateTime? on}) {
    if (isClosed) {
      throw StateError('Bu ödünç zaten kapandı (durum: ${_state.name})');
    }

    _closedOn = on ?? DateTime.now();
    final fine = accruedFine;

    _state = LoanState.returned;
    _book._placeBackOnShelf();
    _member._detachLoan(this, fine);

    _checkInvariants();
    return fine;
  }

  Money reportLost() {
    if (isClosed) {
      throw StateError('Kapanmış ödünç kayıp bildirilemez');
    }

    _closedOn = DateTime.now();
    const replacementCost = Money(15000); // 150,00 ₺
    final fine = accruedFine + replacementCost;

    _state = LoanState.lost;
    _book._writeOffCopy();
    _member._detachLoan(this, fine);

    _checkInvariants();
    return fine;
  }

  /// INVARIANT SUITE
  ///   L1: son teslim tarihi ödünç tarihinden sonra olmalı
  ///   L2: uzatma sayısı sınırı aşamaz
  ///   L3: kapanmış ödüncün kapanış tarihi olmalı, açığın olmamalı
  void _checkInvariants() {
    if (!_dueOn.isAfter(_borrowedOn)) {
      throw StateError('İHLAL L1: teslim tarihi ödünç tarihinden önce');
    }
    if (_renewalCount > maxRenewals) {
      throw StateError('İHLAL L2: uzatma sayısı $_renewalCount');
    }
    if (isClosed && _closedOn == null) {
      throw StateError('İHLAL L3: kapalı ödüncün kapanış tarihi yok');
    }
    if (!isClosed && _closedOn != null) {
      throw StateError('İHLAL L3: açık ödüncün kapanış tarihi var');
    }
  }

  @override
  String toString() {
    switch (_state) {
      case LoanState.returned:
        return '${_book.title} -> ${_member.fullName} | İADE EDİLDİ';
      case LoanState.lost:
        return '${_book.title} -> ${_member.fullName} | KAYIP';
      case LoanState.active:
        if (isOverdue) {
          return '${_book.title} -> ${_member.fullName} | '
              '$daysOverdue gün gecikmiş | ceza $accruedFine';
        }
        return '${_book.title} -> ${_member.fullName} | '
            '$daysRemaining gün kaldı | $renewalsLeft uzatma hakkı';
    }
  }
}

// ############################################################################
//  KATALOG ARAMA
//
//  Parametre tipi Isbn — düz String değil. Bu yüzden yanlış türde bir
//  kimlik vermek DERLENMİYOR. Gün 10'un "misuse resistance" fikri.
// ############################################################################

Book? findInCatalog(List<Book> catalog, Isbn isbn) {
  for (final book in catalog) {
    if (book.isbn == isbn) return book;
  }
  return null;
}
