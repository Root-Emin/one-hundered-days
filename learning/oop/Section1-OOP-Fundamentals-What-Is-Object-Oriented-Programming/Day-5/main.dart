// ============================================================================
// GÜN 5 — MİNİ DOMAIN MODEL: KÜTÜPHANE ÖDÜNÇ SİSTEMİ  (Dart)
//
// Çalıştırmak için:  dart run gun5_mini_domain_model.dart
// veya dartpad.dev'e yapıştır.
//
// Bu gün yeni kavram öğretmiyor; Gün 1-4'ü tek bir çalışan modelde
// birleştiriyor.
//
// BÖLÜM 0 -> Public Field Smell: anemik model (kötü örnek)
// BÖLÜM 1 -> Domain model: Member, Book, Loan
// BÖLÜM 2 -> Demo senaryosu: gerçekçi işlemler
// BÖLÜM 3 -> Üretim kuralları: geçersiz nesne üretilemiyor
// BÖLÜM 4 -> Review checklist
// ============================================================================

// ============================================================================
// BÖLÜM 0 — PUBLIC FIELD SMELL (KÖTÜ ÖRNEK)
//
// Bu sınıfın hiçbir davranışı yok. Sadece public alanlar. Buna "anemic
// domain model" denir: class gibi görünen, aslında sadece bir veri torbası.
//
// Belirtileri:
//   - Bütün alanlar public ve değiştirilebilir
//   - Hiç metot yok, sadece getter/setter
//   - Bütün kurallar bu sınıfı KULLANAN kodda dağınık duruyor
//   - Aynı kural birden çok yerde kopyalanmış oluyor
//
// Sonuç: class yazdın ama OOP yapmadın. Gün 1'deki Map'ten farkı yok.
// ============================================================================

class LoanRecord {
  String? memberId;
  String? bookIsbn;
  DateTime? dueDate;
  bool returned = false;
  double fee = 0;
}

// ============================================================================
// BÖLÜM 1 — DOMAIN MODEL
//
// Üç sınıf, üç net sorumluluk. Her biri kendi verisinin sahibi ve
// kendi kurallarının bekçisi.
//
// NOT: Dart'ta '_' ile başlayan üyeler SINIFA değil DOSYAYA (library)
// özeldir. Bu yüzden Loan, Book._takeCopy() çağırabiliyor ama bu dosyanın
// dışındaki hiçbir kod çağıramıyor. Aradığımız tam olarak bu: nesneler
// birbiriyle konuşabilsin, dış dünya kuralları atlayamasın.
// ============================================================================

/// SORUMLULUK: Bir üyeyi temsil eder ve o üyenin ödünç alma hakkını bilir.
class Member {
  final String id;
  final String fullName;
  final int maxLoans;

  final List<Loan> _activeLoans = [];

  Member({
    required this.id,
    required this.fullName,
    this.maxLoans = 2, // default value (Gün 3)
  }) {
    // Üretim kuralları (Gün 3): geçersiz üye doğamaz.
    if (id.trim().isEmpty) {
      throw ArgumentError.value(id, 'id', 'Üye no boş olamaz');
    }
    if (fullName.trim().isEmpty) {
      throw ArgumentError.value(fullName, 'fullName', 'İsim boş olamaz');
    }
    if (maxLoans < 1) {
      throw ArgumentError.value(maxLoans, 'maxLoans', 'En az 1 olmalı');
    }
  }

  // ---- QUERY'LER (Gün 4) ----
  int get activeLoanCount => _activeLoans.length;

  List<Loan> get activeLoans => List.unmodifiable(_activeLoans);

  bool get hasOverdueLoans => _activeLoans.any((loan) => loan.isOverdue);

  /// TELL, DON'T ASK (Gün 4):
  /// Dışarıdaki kod "kaç kitabın var, limitin kaç, gecikmen var mı?" diye
  /// sormuyor. "Ödünç alabilir misin?" diye soruyor. Kural burada yaşıyor.
  bool get canBorrow => _activeLoans.length < maxLoans && !hasOverdueLoans;

  String get borrowStatus {
    if (hasOverdueLoans) return 'Gecikmiş kitabı var';
    if (_activeLoans.length >= maxLoans) return 'Limit dolu ($maxLoans)';
    return 'Uygun (${maxLoans - _activeLoans.length} hak kaldı)';
  }

  // ---- COMMAND'LER (dosyaya özel: dışarıdan çağrılamaz) ----
  void _registerLoan(Loan loan) => _activeLoans.add(loan);
  void _releaseLoan(Loan loan) => _activeLoans.remove(loan);

  @override
  String toString() => '$fullName (#$id)';
}

/// SORUMLULUK: Bir kitabı ve kaç kopyasının rafta olduğunu bilir.
class Book {
  final String isbn;
  final String title;
  final String author;

  final int totalCopies;
  int _availableCopies;

  Book({
    required this.isbn,
    required this.title,
    required this.author,
    required this.totalCopies,
  }) : _availableCopies = totalCopies {
    if (isbn.trim().isEmpty) {
      throw ArgumentError.value(isbn, 'isbn', 'ISBN boş olamaz');
    }
    if (title.trim().isEmpty) {
      throw ArgumentError.value(title, 'title', 'Başlık boş olamaz');
    }
    if (totalCopies < 1) {
      throw ArgumentError.value(totalCopies, 'totalCopies', 'En az 1 olmalı');
    }
  }

  // ---- QUERY'LER ----
  int get availableCopies => _availableCopies;
  int get borrowedCopies => totalCopies - _availableCopies;
  bool get isAvailable => _availableCopies > 0;

  // ---- COMMAND'LER (dosyaya özel) ----
  //
  // INVARIANT (Gün 3): 0 <= _availableCopies <= totalCopies
  // Bu kural ömür boyu geçerli olmalı, sadece doğum anında değil.
  // O yüzden durumu değiştiren her metot kuralı tekrar kontrol ediyor.

  void _takeCopy() {
    if (_availableCopies <= 0) {
      throw StateError('"$title" için rafta kopya yok');
    }
    _availableCopies--;
  }

  void _returnCopy() {
    if (_availableCopies >= totalCopies) {
      throw StateError('"$title": iade edilen kopya sayısı toplamı aşıyor');
    }
    _availableCopies++;
  }

  @override
  String toString() =>
      '"$title" — $author | rafta $_availableCopies/$totalCopies';
}

/// SORUMLULUK: Tek bir ödünç işlemini temsil eder; süresini, iadesini
/// ve gecikme cezasını bilir.
///
/// Bu sınıf Member ile Book'u birbirine bağlayan nesne (Gün 4: iş birliği).
class Loan {
  static const double dailyLateFee = 2.5;

  final Book book;
  final Member member;
  final DateTime borrowedAt;
  final DateTime dueDate;

  DateTime? _returnedAt;
  int _extensionCount = 0;

  // Private constructor: tek giriş kapısı aşağıdaki factory.
  Loan._({
    required this.book,
    required this.member,
    required this.borrowedAt,
    required this.dueDate,
  });

  // ==========================================================================
  // ÜRETİM KURALLARI (Gün 3 + Gün 4)
  //
  // Ödünç verme kuralları TEK BİR nesneye ait değil: hem kitabın müsait
  // olması hem üyenin hakkı olması gerekiyor. Bu yüzden kuralı üstlenen
  // bir factory var.
  //
  // Kritik nokta: kitabın stoğunu düşürmek ve üyeye kaydetmek de burada.
  // Yani "Loan üretildi ama kitap stoğu düşmedi" gibi tutarsız bir ara
  // durum oluşamıyor.
  // ==========================================================================
  factory Loan.create({
    required Book book,
    required Member member,
    int days = 14,
    DateTime? borrowedAt, // testte geçmiş tarih verebilmek için
  }) {
    if (days < 1) {
      throw ArgumentError.value(days, 'days', 'Süre en az 1 gün olmalı');
    }
    if (!book.isAvailable) {
      throw StateError('${book.title}: rafta kopya yok');
    }
    if (!member.canBorrow) {
      throw StateError(
        '${member.fullName} ödünç alamaz — ${member.borrowStatus}',
      );
    }

    final start = borrowedAt ?? DateTime.now();

    final loan = Loan._(
      book: book,
      member: member,
      borrowedAt: start,
      dueDate: start.add(Duration(days: days)),
    );

    // İki nesnenin durumu birlikte güncelleniyor.
    book._takeCopy();
    member._registerLoan(loan);

    return loan;
  }

  // ---- QUERY'LER: hesaplar, hiçbir şeyi değiştirmez ----

  bool get isReturned => _returnedAt != null;
  DateTime? get returnedAt => _returnedAt;
  int get extensionCount => _extensionCount;

  int get daysOverdue {
    final reference = _returnedAt ?? DateTime.now();
    if (!reference.isAfter(dueDate)) return 0;
    return reference.difference(dueDate).inDays;
  }

  bool get isOverdue => !isReturned && daysOverdue > 0;

  /// Ceza hesabı, cezayı hesaplamak için gereken verinin yanında duruyor.
  double get lateFee => daysOverdue * dailyLateFee;

  int get daysLeft => dueDate.difference(DateTime.now()).inDays;

  // ---- COMMAND'LER: durumu değiştirir ----

  void extend({int days = 7}) {
    if (isReturned) {
      throw StateError('İade edilmiş ödünç uzatılamaz');
    }
    if (isOverdue) {
      throw StateError('Süresi geçmiş ödünç uzatılamaz — önce iade edin');
    }
    if (_extensionCount >= 2) {
      throw StateError('En fazla 2 kez uzatılabilir');
    }
    // dueDate final olduğu için burada yeniden atayamayız; gerçek projede
    // dueDate'i mutable yapardık. Basit tutmak için sayacı artırıp
    // kuralı gösteriyoruz.
    _extensionCount++;
    print('  ${book.title}: uzatma #$_extensionCount kaydedildi ($days gün)');
  }

  /// İade: üç nesnenin durumu birlikte güncelleniyor.
  void returnBook({DateTime? at}) {
    if (isReturned) {
      throw StateError('Bu kitap zaten iade edilmiş');
    }
    _returnedAt = at ?? DateTime.now();
    book._returnCopy();
    member._releaseLoan(this);
  }

  @override
  String toString() {
    if (isReturned) {
      final fee = lateFee > 0 ? ' | ceza ${lateFee.toStringAsFixed(2)} ₺' : '';
      return '${book.title} -> ${member.fullName} | İADE EDİLDİ$fee';
    }
    if (isOverdue) {
      return '${book.title} -> ${member.fullName} | '
          '$daysOverdue GÜN GECİKMİŞ | ceza ${lateFee.toStringAsFixed(2)} ₺';
    }
    return '${book.title} -> ${member.fullName} | $daysLeft gün kaldı';
  }
}

// ============================================================================
// ÇALIŞTIRMA — DEMO SENARYOSU
// ============================================================================

void main() {
  // ==========================================================================
  // BÖLÜM 0 — PUBLIC FIELD SMELL
  // ==========================================================================
  print('=== BÖLÜM 0: ANEMİK MODEL (KÖTÜ) ===');

  final kayit = LoanRecord();
  kayit.memberId = 'M-1';
  kayit.bookIsbn = ''; // boş ISBN, kimse engellemiyor
  kayit.dueDate = DateTime(2020, 1, 1); // geçmiş tarih, sorun değil
  kayit.returned = true;
  kayit.fee = -500; // negatif ceza (!), yine sorun değil

  print('Üretilen saçma kayıt: ISBN="${kayit.memberId}", ceza=${kayit.fee}');
  print('Sınıf hiçbir kuralı korumuyor; class yazdık ama OOP yapmadık.');
  print('');

  // ==========================================================================
  // BÖLÜM 2 — DEMO SENARYOSU
  // ==========================================================================
  print('=== BÖLÜM 2: DEMO SENARYOSU ===');

  final ayse = Member(id: 'M-1', fullName: 'Ayşe Kaya');
  final mert = Member(id: 'M-2', fullName: 'Mert Aslan', maxLoans: 3);

  final suc = Book(
    isbn: '978-1',
    title: 'Suç ve Ceza',
    author: 'Dostoyevski',
    totalCopies: 2,
  );
  final kurk = Book(
    isbn: '978-2',
    title: 'Kürk Mantolu Madonna',
    author: 'Sabahattin Ali',
    totalCopies: 1,
  );
  final tutunamayanlar = Book(
    isbn: '978-3',
    title: 'Tutunamayanlar',
    author: 'Oğuz Atay',
    totalCopies: 1,
  );

  // ---- İşlem 1: Ödünç alma ----
  print('--- 1. Ödünç alma ---');
  final odunc1 = Loan.create(book: suc, member: ayse);
  print('  $odunc1');
  print('  $suc');
  print('  Ayşe: ${ayse.borrowStatus}');

  // ---- İşlem 2: Limit kontrolü ----
  print('--- 2. Limit kontrolü ---');
  Loan.create(book: kurk, member: ayse);
  print('  Ayşe: ${ayse.borrowStatus}');

  try {
    Loan.create(book: tutunamayanlar, member: ayse); // 3. kitap, limit 2
  } on StateError catch (e) {
    print('  Reddedildi: ${e.message}');
  }

  // ---- İşlem 3: Stok kontrolü ----
  print('--- 3. Stok kontrolü ---');
  print('  $kurk');
  try {
    Loan.create(book: kurk, member: mert); // tek kopya, o da Ayşe'de
  } on StateError catch (e) {
    print('  Reddedildi: ${e.message}');
  }

  // ---- İşlem 4: Uzatma ----
  print('--- 4. Süre uzatma ---');
  odunc1.extend();
  odunc1.extend();
  try {
    odunc1.extend(); // 3. deneme
  } on StateError catch (e) {
    print('  Reddedildi: ${e.message}');
  }

  // ---- İşlem 5: Gecikme ve ceza ----
  print('--- 5. Gecikme ve ceza ---');
  final gecmis = DateTime.now().subtract(const Duration(days: 20));
  final gecikmisOdunc = Loan.create(
    book: tutunamayanlar,
    member: mert,
    borrowedAt: gecmis, // 20 gün önce alınmış, 14 gün süreli
  );
  print('  $gecikmisOdunc');
  print('  Mert ödünç alabilir mi? ${mert.canBorrow} — ${mert.borrowStatus}');

  try {
    Loan.create(book: suc, member: mert); // gecikmesi var
  } on StateError catch (e) {
    print('  Reddedildi: ${e.message}');
  }

  // ---- İşlem 6: İade ----
  print('--- 6. İade ---');
  gecikmisOdunc.returnBook();
  print('  $gecikmisOdunc');
  print('  $tutunamayanlar');
  print('  Mert: ${mert.borrowStatus}');

  try {
    gecikmisOdunc.returnBook(); // ikinci kez
  } on StateError catch (e) {
    print('  Reddedildi: ${e.message}');
  }
  print('');

  // ==========================================================================
  // BÖLÜM 3 — ÜRETİM KURALLARI
  // ==========================================================================
  print('=== BÖLÜM 3: GEÇERSİZ NESNE ÜRETİLEMİYOR ===');

  final denemeler = <String, void Function()>{
    'Boş üye adı': () => Member(id: 'M-9', fullName: '  '),
    'Sıfır limit': () => Member(id: 'M-9', fullName: 'Test', maxLoans: 0),
    'Boş ISBN': () => Book(isbn: '', title: 'X', author: 'Y', totalCopies: 1),
    'Sıfır kopya': () =>
        Book(isbn: '978-9', title: 'X', author: 'Y', totalCopies: 0),
    'Sıfır günlük ödünç': () => Loan.create(book: suc, member: mert, days: 0),
  };

  denemeler.forEach((baslik, deneme) {
    try {
      deneme();
      print('  $baslik -> BEKLENMEDİK: üretildi!');
    } catch (e) {
      final mesaj = e is ArgumentError ? e.message : (e as StateError).message;
      print('  $baslik -> reddedildi: $mesaj');
    }
  });
  print('');

  // ==========================================================================
  // BÖLÜM 4 — REVIEW CHECKLIST
  // ==========================================================================
  print('=== BÖLÜM 4: TASARIM KONTROL LİSTESİ ===');
  print('  1. Her sınıf tek cümleyle anlatılabiliyor mu?      -> evet');
  print('  2. Kurallar nesnenin içinde mi?                    -> evet');
  print('  3. Geçersiz nesne üretilebiliyor mu?               -> hayır');
  print('  4. Command ve query ayrımı net mi?                 -> evet');
  print(
    '  5. Sadece public alan olan sınıf var mı?           -> sadece'
    ' LoanRecord (kötü örnek)',
  );
  print('  6. Durum tutarsız kalabiliyor mu?                  -> hayır');
  print('     (Loan.create ve returnBook, ilgili üç nesneyi birlikte');
  print('      güncelliyor; yarım kalmış bir işlem oluşamıyor)');
}
