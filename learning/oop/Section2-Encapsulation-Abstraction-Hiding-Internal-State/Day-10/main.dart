// ============================================================================
// GÜN 10 — SERTLEŞTİRME  (2/2: DIŞARIDAKİ KOD)
//
// Çalıştırmak için:  dart run gun10_main.dart
// (gun10_kutuphane.dart ile aynı klasörde olmalı)
//
// Bu dosya "dış dünya". Modelin private üyelerine erişimi yok, bu yüzden
// "artık imkânsız" iddiasını gerçekten kanıtlayabiliyoruz.
// ============================================================================

import 'kutuphane.dart';

int _gecen = 0;
int _kalan = 0;

void beklenenHata<T>(String etiket, void Function() eylem) {
  try {
    eylem();
    _kalan++;
    print('  ✗ $etiket  <-- engellenmedi!');
  } catch (e) {
    if (e is T) {
      _gecen++;
      print('  ✓ $etiket');
    } else {
      _kalan++;
      print('  ✗ $etiket  <-- $T bekleniyordu, ${e.runtimeType} geldi');
    }
  }
}

void kontrol(String etiket, bool kosul) {
  if (kosul) {
    _gecen++;
    print('  ✓ $etiket');
  } else {
    _kalan++;
    print('  ✗ $etiket  <-- BAŞARISIZ');
  }
}

void main() {
  // ==========================================================================
  print('=== BÖLÜM 1: DOMAIN DİLİYLE NORMAL KULLANIM ===');

  final suc = Book.catalog(
    isbn: '978-9-7508-0778-1',
    title: 'Suç ve Ceza',
    author: 'Dostoyevski',
    copies: 2,
  );
  final kurk = Book.catalog(
    isbn: '9789753638029',
    title: 'Kürk Mantolu Madonna',
    author: 'Sabahattin Ali',
    copies: 1,
  );

  final ayse = Member.register(memberId: 'M-1001', fullName: 'Ayşe Kaya');
  final mert = Member.register(
    memberId: 'M-1002',
    fullName: 'Mert Aslan',
    borrowingLimit: 3,
  );

  print('  $suc');
  print('  ISBN biçimlendi: ${suc.isbn}');
  print('  $ayse — ${ayse.standing}');
  print('');

  // Bu satırlar kütüphanecinin cümlesi gibi okunuyor:
  final oduncA = suc.lendTo(ayse, period: LoanPeriod.twoWeeks);
  final oduncB = kurk.lendTo(ayse);

  print('  $oduncA');
  print('  $oduncB');
  print('  $suc');
  print('  ${ayse.fullName} — ${ayse.standing}');

  oduncA.renew(by: LoanPeriod.oneMonth);
  print('  Uzatma sonrası: $oduncA');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 2: DERLENMEYEN İŞLEMLER ===');
  print('  Aşağıdaki satırların hepsi yorumda. Açıp derlemeyi dene —');
  print('  aldığın hatalar Gün 10\'un asıl dersidir.');
  print('');

  // --- Alanlara doğrudan yazma ---
  // suc._copiesOnShelf = 999;        // '_copiesOnShelf' bu kütüphanede yok
  // suc.copiesOnShelf = 999;         // setter yok, sadece getter var
  // ayse._openLoans.clear();         // '_openLoans' bu kütüphanede yok
  // oduncA._state = LoanState.returned;  // '_state' bu kütüphanede yok

  // --- Doğrulamayı atlayarak nesne üretme ---
  // final sahte = Isbn._('abc');     // private constructor
  // final sahteOdunc = Loan._(...);  // private constructor
  //   -> Ödünç üretmenin TEK yolu book.lendTo(). Yani "kitabın stoğu
  //      düşmeden ödünç oluşturmak" fiziksel olarak mümkün değil.

  // --- Yanlış türde kimlik vermek (primitive obsession'a karşı) ---
  // findInCatalog([suc, kurk], ayse.id);   // MemberId != Isbn
  // findInCatalog([suc, kurk], '9789753638029');  // String != Isbn
  //   -> Gün 5'te ikisi de düz String'di ve bu hata sessizce derlenirdi.

  // --- Var olmayan ödünç süresi ---
  // suc.lendTo(mert, period: 5000);              // int != LoanPeriod
  // oduncA.renew(by: Duration(days: 3650));      // Duration != LoanPeriod
  //   -> Geçersiz süre artık İFADE EDİLEMİYOR; doğrulamaya bile gerek yok.

  print('  9 satır yorumda, hiçbiri derlenmiyor.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 3: ÇALIŞMA ANINDA ENGELLENENLER ===');

  print('--- Geçersiz kimlikler ---');
  beklenenHata<FormatException>(
    'Kısa ISBN reddedilir',
    () => Book.catalog(isbn: '123', title: 'X', author: 'Y', copies: 1),
  );
  beklenenHata<FormatException>(
    'Harf içeren ISBN reddedilir',
    () =>
        Book.catalog(isbn: '978975363802X', title: 'X', author: 'Y', copies: 1),
  );
  beklenenHata<FormatException>(
    'Biçimsiz üye no reddedilir',
    () => Member.register(memberId: '1001', fullName: 'Test'),
  );

  print('--- Kataloglama kuralları ---');
  beklenenHata<ArgumentError>(
    'Boş başlık reddedilir',
    () => Book.catalog(
      isbn: '9789753638029',
      title: '  ',
      author: 'Y',
      copies: 1,
    ),
  );
  beklenenHata<ArgumentError>(
    'Sıfır kopya reddedilir',
    () =>
        Book.catalog(isbn: '9789753638029', title: 'X', author: 'Y', copies: 0),
  );
  beklenenHata<ArgumentError>(
    'Tek harflik isim reddedilir',
    () => Member.register(memberId: 'M-9999', fullName: 'A'),
  );

  print('--- Koleksiyonlara müdahale ---');
  beklenenHata<UnsupportedError>(
    'openLoans listesine ekleme engellenir',
    () => ayse.openLoans.add(oduncA),
  );
  beklenenHata<UnsupportedError>(
    'openLoans temizlenemez',
    () => ayse.openLoans.clear(),
  );

  print('--- Ödünç kuralları ---');
  beklenenHata<StateError>(
    'Limit dolu üyeye ödünç verilmez',
    () => suc.lendTo(ayse),
  ); // limit 2, ikisi de dolu

  final tek = Book.catalog(
    isbn: '9780000000019',
    title: 'Tek Nüsha',
    author: 'Z',
    copies: 1,
  );
  tek.lendTo(mert);
  beklenenHata<StateError>(
    'Rafta olmayan kitap ödünç verilmez',
    () => tek.lendTo(ayse),
  );

  print('--- Uzatma kuralları ---');
  oduncB.renew();
  oduncB.renew();
  beklenenHata<StateError>('Üçüncü uzatma reddedilir', () => oduncB.renew());
  kontrol('Uzatma hakkı sıfırlandı', oduncB.renewalsLeft == 0);

  final gecmis = DateTime.now().subtract(const Duration(days: 30));
  Book.catalog(
    isbn: '9780000000026',
    title: 'Geciken Kitap',
    author: 'W',
    copies: 1,
  )..lendTo(mert, on: gecmis);
  final gecikmisOdunc = mert.openLoans.last;

  kontrol('Gecikme tespit edildi', gecikmisOdunc.isOverdue);
  beklenenHata<StateError>(
    'Gecikmiş ödünç uzatılamaz',
    () => gecikmisOdunc.renew(),
  );
  beklenenHata<StateError>(
    'Gecikmesi olan üye ödünç alamaz',
    () => kurk.lendTo(mert),
  );

  print('--- Kapanmış ödünç ---');
  final ceza = gecikmisOdunc.returnToShelf();
  print('  İade edildi, gecikme cezası: $ceza');
  kontrol('Durum returned oldu', gecikmisOdunc.state == LoanState.returned);
  kontrol('Ceza üyeye işlendi', mert.paidFines > Money.zero);

  beklenenHata<StateError>(
    'İkinci kez iade edilemez',
    () => gecikmisOdunc.returnToShelf(),
  );
  beklenenHata<StateError>(
    'Kapanmış ödünç uzatılamaz',
    () => gecikmisOdunc.renew(),
  );
  beklenenHata<StateError>(
    'Kapanmış ödünç kayıp bildirilemez',
    () => gecikmisOdunc.reportLost(),
  );
  print('');

  // ==========================================================================
  print('=== BÖLÜM 4: DEĞER NESNELERİ ===');

  final isbn1 = Isbn.parse('978-975-363-802-9');
  final isbn2 = Isbn.parse('9789753638029');
  kontrol('Farklı yazım, aynı ISBN', isbn1 == isbn2);
  kontrol('Katalogda bulunuyor', findInCatalog([suc, kurk], isbn1) == kurk);

  const a = Money(250);
  const b = Money(250);
  kontrol('Money değere göre eşit', a == b);
  kontrol('Money toplanabiliyor', (a + b) == const Money(500));
  kontrol('Money çarpılabiliyor', (a * 4) == const Money(1000));
  print('  Ceza hesabı artık düz double değil: ${a * 4}');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 5: BEFORE/AFTER — ARTIK İMKÂNSIZ OLANLAR ===');

  const notlar = [
    'Kitabın raf sayısını dışarıdan değiştirip stok kaydını bozmak.',
    'Stok düşmeden veya üyeye kaydolmadan bir Loan nesnesi üretmek.',
    'Üye no beklenen yere ISBN (ya da herhangi bir String) vermek.',
    'İade edilmiş bir ödüncü tekrar iade etmek veya uzatmak.',
    'Var olmayan bir ödünç süresi istemek (5000 gün, -3 gün).',
    'openLoans listesine dışarıdan ödünç ekleyip limiti aşmak.',
    'Gecikmiş kitabı olan üyeye yeni kitap verdirmek.',
    'Cezayı yanlış para biriminde veya kuruş hassasiyeti olmadan tutmak.',
  ];

  for (var i = 0; i < notlar.length; i++) {
    print('  ${i + 1}. ${notlar[i]}');
  }

  print('');
  print('  Dikkat: bunların çoğu "kontrol ekledik" diye engellenmiyor.');
  print('  Üçü DERLEME ZAMANINDA engelleniyor (tip sistemi), ikisi de');
  print('  hiç ifade edilemediği için ortaya çıkamıyor. En iyi doğrulama,');
  print('  yazmana gerek kalmayan doğrulamadır.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 6: İSİMLENDİRME — GÜN 5\'TEN GÜN 10\'A ===');

  const isimler = [
    ['Loan.create(book:, member:, days:)', 'book.lendTo(member, period:)'],
    ['loan.extend(days: 7)', 'loan.renew(by: LoanPeriod.twoWeeks)'],
    ['loan.markReturned()', 'loan.returnToShelf()'],
    ['new Member(...)', 'Member.register(...)'],
    ['new Book(...)', 'Book.catalog(...)'],
    ['member.canBorrow (sadece bool)', 'member.standing (sebebi söyler)'],
    ['loan.lateFee (double)', 'loan.accruedFine (Money)'],
    ['book.availableCopies', 'book.copiesOnShelf'],
  ];

  print('  ${'GÜN 5 — TEKNİK'.padRight(40)}GÜN 10 — DOMAIN DİLİ');
  print('  ${'-' * 40}${'-' * 36}');
  for (final cift in isimler) {
    print('  ${cift[0].padRight(40)}${cift[1]}');
  }

  print('');
  print('  Ölçüt: bu satırları bir kütüphaneciye okusan anlar mıydı?');
  print('  "Kürk Mantolu Madonna\'yı Ayşe\'ye iki haftalığına ödünç ver,');
  print('   sonra bir ay uzat, sonra rafa geri koy." — evet, anlardı.');
  print('');

  print('=== SONUÇ ===');
  print('  Geçen: $_gecen');
  print('  Kalan: $_kalan');
  print(
    _kalan == 0
        ? '  Model sertleşti: yanlış kullanım zor, doğru kullanım kolay.'
        : '  DİKKAT: $_kalan kontrol başarısız!',
  );
}
