// ============================================================================
// GÜN 2 — CLASS, INSTANCE, METHOD, IDENTITY  (Dart)
//
// Çalıştırmak için:  dart run gun2_class_ve_instance.dart
// veya dartpad.dev'e yapıştır.
//
// BÖLÜM 1 -> Sınıf tanımı (Book)
// BÖLÜM 2 -> Birden fazla instance üretmek ve bağımsızlıklarını görmek
// BÖLÜM 3 -> Identity: eşit değerler != aynı nesne
// BÖLÜM 4 -> (Bonus) Değer eşitliği: == ve hashCode'u kendin yazmak
// ============================================================================

// ============================================================================
// BÖLÜM 1 — SINIF TANIMI
// ============================================================================

/// Bir kitabı ve okuma ilerlemesini temsil eder.
class Book {
  // ---- INSTANCE ATTRIBUTE'LARI ----
  // Bu alanların HER NESNE İÇİN AYRI bir kopyası vardır.
  // 100 tane Book üretirsen bellekte 100 tane ayrı 'title' olur.
  final String title;
  final String author;
  final int totalPages;

  // Değişebilen durum (mutable state). '_' ile dışarıya kapalı.
  int _currentPage = 0;

  // ---- STATIC (CLASS-LEVEL) ATTRIBUTE ----
  // 'static' olan alan nesneye değil SINIFA aittir. Tek bir tane vardır,
  // bütün Book nesneleri onu paylaşır. Instance state ile farkını
  // Bölüm 2'de göreceksin.
  static int _createdCount = 0;

  // ---- CONSTRUCTOR ----
  // Nesne doğarken çalışır. Süslü parantezli gövde ile ek iş yapabilirsin.
  Book({required this.title, required this.author, required this.totalPages}) {
    _createdCount++;
  }

  // ---- GETTER'LAR ----
  // Bunlar depolanan veri değil, HESAPLANAN veri. Her çağrıda yeniden
  // hesaplanır, o yüzden asla güncel olmayan bir değer göremezsin.
  int get currentPage => _currentPage;
  int get remainingPages => totalPages - _currentPage;
  bool get isFinished => _currentPage >= totalPages;

  double get progressPercent {
    if (totalPages == 0) return 0;
    return (_currentPage / totalPages) * 100;
  }

  /// Sınıf seviyesindeki sayaca erişim. Nesne üzerinden değil,
  /// Book.createdCount şeklinde çağrılır.
  static int get createdCount => _createdCount;

  // ---- METOTLAR ----
  // Dikkat: hiçbiri 'hangi kitap' diye parametre almıyor.
  // Üzerinde çalıştıkları nesne zaten 'this'.

  /// Belirtilen sayıda sayfa okur. Toplam sayfayı aşamaz.
  void read(int pages) {
    if (pages <= 0) {
      print('Geçersiz sayfa sayısı: $pages');
      return;
    }
    if (isFinished) {
      print('"$title" zaten bitmiş.');
      return;
    }

    _currentPage += pages;
    if (_currentPage > totalPages) {
      _currentPage = totalPages; // taşmayı nesnenin kendisi engelliyor
    }

    print('"$title" okundu: ${_currentPage}/$totalPages');
    if (isFinished) {
      print('  -> "$title" tamamlandı!');
    }
  }

  /// Baştan başlar.
  void reset() {
    _currentPage = 0;
    print('"$title" sıfırlandı.');
  }

  /// Nesnenin kendi verisini kullanarak okunabilir bir özet üretir.
  String describe() {
    final percent = progressPercent.toStringAsFixed(1);
    final status = isFinished ? 'BİTTİ' : '$remainingPages sayfa kaldı';
    return '"$title" — $author | ${_currentPage}/$totalPages (%$percent) | $status';
  }

  /// toString() her Dart nesnesinde vardır; ezersen print(nesne) güzelleşir.
  @override
  String toString() => describe();
}

// ============================================================================
// BÖLÜM 4 (sınıf tanımı) — DEĞER EŞİTLİĞİ
//
// Dart'ta varsayılan '==' KİMLİK karşılaştırır: "bu ikisi bellekte aynı
// nesne mi?" Bazen istediğin bu değildir. Aşağıdaki Isbn sınıfı,
// "değerleri aynıysa eşit sayılsın" diyor.
// ============================================================================

class Isbn {
  final String code;

  const Isbn(this.code);

  @override
  bool operator ==(Object other) {
    if (identical(this, other)) return true; // aynı nesneyse zaten eşit
    return other is Isbn && other.code == code; // değilse değerlere bak
  }

  // KURAL: == 'i ezersen hashCode'u da ezmek ZORUNDASIN.
  // Yoksa Set ve Map içinde nesnen yanlış davranır.
  @override
  int get hashCode => code.hashCode;

  @override
  String toString() => 'ISBN($code)';
}

// ============================================================================
// ÇALIŞTIRMA
// ============================================================================

void main() {
  // ==========================================================================
  // BÖLÜM 2 — INSTANCE ÜRETMEK
  // ==========================================================================
  print('=== BÖLÜM 2: FARKLI INSTANCE\'LAR ===');

  // Tek bir 'Book' sınıfı (kalıp) var, ama iki ayrı nesne (instance) üretiyoruz.
  final book1 = Book(
    title: 'Tutunamayanlar',
    author: 'Oğuz Atay',
    totalPages: 724,
  );

  final book2 = Book(
    title: 'Kürk Mantolu Madonna',
    author: 'Sabahattin Ali',
    totalPages: 160,
  );

  print(book1.describe());
  print(book2.describe());
  print('');

  // ---- Metotları HER instance üzerinde ayrı ayrı çağırıyoruz ----
  print('--- Okuma ---');
  book1.read(100);
  book2.read(40);
  book2.read(30);
  print('');

  // ---- BAĞIMSIZLIK KANITI ----
  // book1'i okumak book2'yi hiç etkilemedi. İkisinin _currentPage'i ayrı.
  print('--- Durum kontrolü ---');
  print('book1 -> ${book1.currentPage} sayfa, kalan: ${book1.remainingPages}');
  print('book2 -> ${book2.currentPage} sayfa, kalan: ${book2.remainingPages}');
  print('');

  // ---- INSTANCE STATE vs STATIC STATE ----
  // currentPage her nesnede ayrı. createdCount ise TEK, hepsi paylaşıyor.
  print('Şu ana kadar üretilen Book sayısı: ${Book.createdCount}');
  print('');

  // ---- Sınır kontrolünün nesnenin içinde olmasının faydası ----
  print('--- Taşma denemesi ---');
  book2.read(500); // 160 sayfalık kitaba 500 sayfa
  print(book2.describe());
  book2.read(10); // bitmiş kitaba tekrar okuma
  print('');

  // ==========================================================================
  // BÖLÜM 3 — IDENTITY
  // ==========================================================================
  print('=== BÖLÜM 3: IDENTITY (KİMLİK) ===');

  // Bu iki kitabın BÜTÜN değerleri birebir aynı.
  final ikiz1 = Book(
    title: 'Sefiller',
    author: 'Victor Hugo',
    totalPages: 1400,
  );
  final ikiz2 = Book(
    title: 'Sefiller',
    author: 'Victor Hugo',
    totalPages: 1400,
  );

  print('Değerler aynı mı? -> başlık: ${ikiz1.title == ikiz2.title}');
  print('ikiz1 == ikiz2   -> ${ikiz1 == ikiz2}'); // false
  print('identical(...)   -> ${identical(ikiz1, ikiz2)}'); // false

  // Neden false? Çünkü bunlar bellekte İKİ AYRI nesne.
  // Aynı tarife göre yapılmış iki ayrı kek gibi. Tarif aynı, kekler ayrı.
  ikiz1.read(50);
  print(
    'ikiz1 okundu -> ikiz1: ${ikiz1.currentPage}, ikiz2: ${ikiz2.currentPage}',
  );
  print('');

  // ---- BURASI ÖNEMLİ: atama KOPYA ÜRETMEZ ----
  final ayniKitap = ikiz1; // yeni nesne değil, aynı nesneye ikinci bir isim

  print(
    'identical(ayniKitap, ikiz1) -> ${identical(ayniKitap, ikiz1)}',
  ); // true
  ayniKitap.read(100);
  print('ayniKitap üzerinden okudum -> ikiz1: ${ikiz1.currentPage}');
  // ikiz1 de değişti, çünkü ikisi de AYNI nesneyi işaret ediyor.
  print('');

  // ==========================================================================
  // BÖLÜM 4 — DEĞER EŞİTLİĞİ (BONUS)
  // ==========================================================================
  print('=== BÖLÜM 4: DEĞER EŞİTLİĞİ ===');

  final isbnA = Isbn('978-975-539-000-0');
  final isbnB = Isbn('978-975-539-000-0');

  print('isbnA == isbnB          -> ${isbnA == isbnB}'); // true
  print('identical(isbnA, isbnB) -> ${identical(isbnA, isbnB)}'); // false

  // Yani: eşit sayılıyorlar ama hâlâ iki ayrı nesneler.
  // "Eşitlik" ve "kimlik" farklı sorular. Book'ta == 'i ezmediğimiz için
  // orada eşitlik = kimlik oldu; Isbn'de ezdik, ayrıştı.

  // hashCode'u doğru ezmenin faydası: Set tekrarı eler.
  final kumeIsbn = {isbnA, isbnB};
  print('Set<Isbn> boyutu -> ${kumeIsbn.length}'); // 1

  final kumeBook = {ikiz1, ikiz2};
  print('Set<Book> boyutu -> ${kumeBook.length}'); // 2 (== ezilmedi)
}
