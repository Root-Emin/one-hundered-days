// ============================================================================
// GÜN 20 — CAPSTONE  (2/2: DEMO, TESTLER VE DEĞERLENDİRME)
//
// Çalıştırmak için:  dart run gun20_main.dart
// (gun20_kayit_sistemi.dart ile aynı klasörde olmalı)
//
// Bu dosya "dış dünya". Modelin private üyelerine erişimi yok —
// bu yüzden kapsülleme iddiasını gerçekten kanıtlayabiliyor (Gün 6).
// ============================================================================

import 'kayitSistemi.dart';

int _gecen = 0;
int _kalan = 0;

void kontrol(String etiket, bool kosul, [String detay = '']) {
  if (kosul) {
    _gecen++;
    print('    ✓ $etiket');
  } else {
    _kalan++;
    print('    ✗ $etiket${detay.isEmpty ? "" : "  <-- $detay"}');
  }
}

void beklenenHata<T>(String etiket, void Function() eylem) {
  try {
    eylem();
    _kalan++;
    print('    ✗ $etiket  <-- hata bekleniyordu');
  } catch (e) {
    if (e is T) {
      _gecen++;
      print('    ✓ $etiket');
    } else {
      _kalan++;
      print('    ✗ $etiket  <-- $T bekleniyordu, ${e.runtimeType} geldi');
    }
  }
}

// ############################################################################
//  GENİŞLETME KANITI  (Gün 16: OCP)
//
//  Bu politika, modele SONRADAN eklendi ve gun20_kayit_sistemi.dart'ta
//  TEK SATIR değişmedi. Registrar onu tanımıyor, yine de uyguluyor.
// ############################################################################

/// YENİ KURAL: akademik durumu zayıf öğrenci 20 krediyi aşamaz.
class ProbationCreditPolicy implements EnrollmentPolicy {
  final int probationLimit;
  const ProbationCreditPolicy({this.probationLimit = 20});

  @override
  String get name => 'Şartlı öğrenci sınırı ($probationLimit)';

  @override
  PolicyResult check(Student s, Course c, Term t) {
    if (s.transcript.isInGoodStanding) return const PolicyResult.allow();

    final yeni = s.activeCredits + c.credits;
    if (yeni > probationLimit) {
      return PolicyResult.deny(
        'Şartlı öğrenci $probationLimit krediyi aşamaz (GNO '
        '${s.transcript.gpa.toStringAsFixed(2)})',
      );
    }
    return const PolicyResult.allow();
  }
}

/// YENİ KURAL: yaz döneminde en fazla iki ders.
class SummerCourseLimitPolicy implements EnrollmentPolicy {
  final int maxCourses;
  const SummerCourseLimitPolicy({this.maxCourses = 2});

  @override
  String get name => 'Yaz dönemi ders sınırı ($maxCourses)';

  @override
  PolicyResult check(Student s, Course c, Term t) {
    if (t.semester != Semester.summer) return const PolicyResult.allow();

    final yazAktif = s.activeEnrollments.where((e) => e.term == t).length;
    if (yazAktif >= maxCourses) {
      return PolicyResult.deny('Yaz döneminde en fazla $maxCourses ders');
    }
    return const PolicyResult.allow();
  }
}

// ============================================================================
// ÇALIŞTIRMA
// ============================================================================

const _guz = Term(2026, Semester.fall);
const _bahar = Term(2027, Semester.spring);
const _yaz = Term(2027, Semester.summer);

void main() {
  // ==========================================================================
  print('=== BÖLÜM 1: KATALOG VE KAYIT ===');

  final mat101 = Course.open(
    code: 'MAT101',
    title: 'Matematik I',
    credits: 6,
    capacity: 3,
  );
  final fiz101 = Course.open(
    code: 'FIZ101',
    title: 'Fizik I',
    credits: 5,
    capacity: 30,
  );
  final mat201 = Course.open(
    code: 'MAT201',
    title: 'Diferansiyel Denklemler',
    credits: 6,
    capacity: 25,
    prerequisites: ['MAT101'],
  );
  final prog101 = Course.open(
    code: 'BIL101',
    title: 'Programlamaya Giriş',
    credits: 8,
    capacity: 40,
  );
  final ing101 = Course.open(
    code: 'ING101',
    title: 'İngilizce I',
    credits: 4,
    capacity: 50,
  );
  final tar101 = Course.open(
    code: 'TAR101',
    title: 'Atatürk İlkeleri',
    credits: 4,
    capacity: 100,
  );
  final kim101 = Course.open(
    code: 'KIM101',
    title: 'Kimya I',
    credits: 5,
    capacity: 40,
  );

  final katalog = [mat101, fiz101, mat201, prog101, ing101, tar101, kim101];
  for (final c in katalog) {
    final onkosul = c.hasPrerequisites
        ? '  önkoşul: ${c.prerequisites.join(", ")}'
        : '';
    print('  $c$onkosul');
  }
  print('');

  final ayse = Student.register(
    studentId: 'S-2026-0001',
    fullName: 'Ayşe Kaya',
    startedIn: _guz,
  );
  final mert = Student.register(
    studentId: 's-2026-0002',
    fullName: 'Mert Aslan',
    startedIn: _guz,
  );
  final elif = Student.register(
    studentId: 'S-2026-0003',
    fullName: 'Elif Şahin',
    startedIn: _guz,
  );

  print('  Öğrenciler:');
  for (final s in [ayse, mert, elif]) {
    print('    $s — ${s.transcript.summary()}');
  }
  print('  (Mert\'in numarası küçük harfle girildi, değer nesnesi');
  print('   normalize etti: ${mert.id})');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 2: GÜZ DÖNEMİ ===');

  final guzKayit = Registrar(policy: standardPolicy(), term: _guz);

  final sonuc = guzKayit.enrollAll(ayse, [mat101, fiz101, prog101, ing101]);
  print(
    '  ${ayse.fullName} — ${sonuc.enrolled.length} kayıt, '
    '${sonuc.rejected.length} red',
  );
  for (final e in sonuc.enrolled) {
    print('    ✓ $e');
  }
  for (final r in sonuc.rejected) {
    print('    ✗ $r');
  }
  print('  Aktif kredi: ${ayse.activeCredits}');
  print('');

  print('  Reddedilme senaryoları:');

  // Önkoşul
  final onkosulKarari = guzKayit.canEnroll(ayse, mat201);
  print('    MAT201 -> ${onkosulKarari.reason}');

  // Kredi sınırı
  final krediKarari = guzKayit.canEnroll(ayse, kim101);
  print('    KIM101 -> ${krediKarari.reason ?? "izin verildi"}');

  // Mükerrer
  final mukerrer = guzKayit.canEnroll(ayse, mat101);
  print('    MAT101 -> ${mukerrer.reason}');

  // Kontenjan
  guzKayit.enroll(mert, mat101);
  guzKayit.enroll(elif, mat101);
  print('    MAT101 kontenjan: ${mat101.seatsLeft}/${mat101.capacity} boş');

  final dorduncu = Student.register(
    studentId: 'S-2026-0004',
    fullName: 'Can Yıldız',
    startedIn: _guz,
  );
  final kontenjan = guzKayit.canEnroll(dorduncu, mat101);
  print('    ${dorduncu.fullName} için MAT101 -> ${kontenjan.reason}');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 3: DÖNEM SONU VE ÖNKOŞULUN AÇILMASI ===');

  final ayseKayitlari = {
    for (final e in ayse.activeEnrollments) e.course.code.value: e,
  };

  ayseKayitlari['MAT101']!.complete(Grade.of(88));
  ayseKayitlari['FIZ101']!.complete(Grade.of(72));
  ayseKayitlari['BIL101']!.complete(Grade.of(95));
  ayseKayitlari['ING101']!.complete(Grade.of(45)); // kaldı

  print('  ${ayse.fullName} transkripti:');
  for (final e in ayse.enrollments) {
    print('    $e');
  }
  print('  ${ayse.transcript.summary()}');
  print(
    '  Akademik durum: '
    '${ayse.transcript.isInGoodStanding ? "iyi" : "şartlı"}',
  );
  print('  Aktif kredi (dönem kapandı): ${ayse.activeCredits}');
  print('');

  final baharKayit = Registrar(policy: standardPolicy(), term: _bahar);
  final mat201Karari = baharKayit.canEnroll(ayse, mat201);
  print(
    '  Bahar döneminde MAT201: '
    '${mat201Karari.allowed ? "izin verildi" : mat201Karari.reason}',
  );
  print('  (MAT101 geçildiği için önkoşul artık sağlanıyor.)');

  final ing101Tekrar = baharKayit.canEnroll(ayse, ing101);
  print(
    '  ING101 tekrar: '
    '${ing101Tekrar.allowed ? "izin verildi (kaldığı için)" : ing101Tekrar.reason}',
  );

  baharKayit.enroll(ayse, mat201);
  baharKayit.enroll(ayse, ing101);
  print('  Bahar aktif kredi: ${ayse.activeCredits}');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 4: OCP — YENİ KURAL, SIFIR DÜZENLEME ===');

  // Zayıf transkriptli bir öğrenci hazırlayalım.
  final zayif = Student.register(
    studentId: 'S-2026-0005',
    fullName: 'Deniz Ak',
    startedIn: _guz,
  );
  final gecmisKayit = Registrar(policy: standardPolicy(), term: _guz);
  gecmisKayit.enroll(zayif, fiz101);
  gecmisKayit.enroll(zayif, tar101);
  zayif.activeEnrollments[0].complete(Grade.of(52));
  zayif.activeEnrollments[0].complete(Grade.of(48));

  print('  ${zayif.fullName}: ${zayif.transcript.summary()}');
  print(
    '  Akademik durum: '
    '${zayif.transcript.isInGoodStanding ? "iyi" : "şartlı"}',
  );
  print('');

  // Yönetmelik değişti: iki yeni kural eklendi.
  final yeniYonetmelik = AllOfPolicy([
    const NoDuplicatePolicy(),
    const AlreadyPassedPolicy(),
    const PrerequisitePolicy(),
    const CapacityPolicy(),
    const CreditLimitPolicy(),
    const ProbationCreditPolicy(), // <-- YENİ
    const SummerCourseLimitPolicy(), // <-- YENİ
  ]);

  final yeniKayit = Registrar(policy: yeniYonetmelik, term: _bahar);
  print('  Yeni yönetmelik: ${yeniYonetmelik.policies.length} kural');

  yeniKayit.enroll(zayif, prog101); // 8 kredi
  yeniKayit.enroll(zayif, ing101); // 4 kredi -> 12
  print('  ${zayif.fullName} aktif kredi: ${zayif.activeCredits}');

  final sartliKarar = yeniKayit.canEnroll(zayif, mat101); // +6 -> 18, geçer
  print(
    '  MAT101 (+6 = 18): '
    '${sartliKarar.allowed ? "izin verildi" : sartliKarar.reason}',
  );
  yeniKayit.enroll(zayif, mat101);

  final asim = yeniKayit.canEnroll(zayif, kim101); // +5 -> 23, şartlı sınırı 20
  print('  KIM101 (+5 = 23): ${asim.reason}');
  print('');

  // Yaz dönemi kuralı
  final yazKayit = Registrar(policy: yeniYonetmelik, term: _yaz);
  yazKayit.enroll(mert, ing101);
  yazKayit.enroll(mert, tar101);
  final yazUcuncu = yazKayit.canEnroll(mert, kim101);
  print('  ${mert.fullName} yaz döneminde 3. ders: ${yazUcuncu.reason}');
  print('');

  print('  DEĞİŞTİRİLEN DOSYA SAYISI: 0');
  print('  EKLENEN SINIF SAYISI: 2 (ProbationCreditPolicy,');
  print('                           SummerCourseLimitPolicy)');
  print('  Registrar, Student, Course bu kuralları tanımıyor.');
  print('  Kural listesine bir eleman eklemek yeterli oldu.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 5: DERLENMEYEN İŞLEMLER (Gün 6, 10) ===');
  print('  Aşağıdaki satırlar yorumda; hiçbiri derlenmiyor.');
  print('  Denemek için açıp derlemeye çalış.');
  print('');

  // mat101._seatsTaken = 0;              // '_seatsTaken' bu kütüphanede yok
  // mat101.seatsTaken = 0;               // setter yok
  // ayse._enrollments.clear();           // private
  // ayse.enrollments.add(...);           // UnmodifiableListView
  // final sahte = Enrollment._(...);     // private constructor
  // final id = StudentId._('X');         // private constructor
  // ayse.hasPassed('MAT101');            // String != CourseCode
  // guzKayit.canEnroll(mat101, ayse);    // parametre sırası — tip hatası
  // Grade.of(150);                       // derlenir ama çalışma anında reddedilir

  const derlemeyenler = [
    'mat101._seatsTaken = 0            (private alan)',
    'mat101.seatsTaken = 0             (setter yok)',
    'ayse._enrollments.clear()         (private alan)',
    'Enrollment._(...)                 (private constructor)',
    'StudentId._("X")                  (private constructor)',
    'ayse.hasPassed("MAT101")          (String != CourseCode)',
    'guzKayit.canEnroll(mat101, ayse)  (parametre sırası tip hatası)',
  ];
  for (final d in derlemeyenler) {
    print('    $d');
  }
  print('');
  print('  Sondan ikincisi Gün 10\'un asıl dersi: değer nesnesi');
  print('  kullanınca yanlış türde kimlik vermek DERLEME ZAMANINDA');
  print('  yakalanıyor. Düz String olsaydı sessizce derlenirdi.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 6: TESTLER ===');

  print('  --- Değer nesneleri ---');
  kontrol(
    'Farklı yazım, aynı ders kodu',
    CourseCode.parse('mat 101') == CourseCode.parse('MAT101'),
  );
  kontrol(
    'Aynı dönem değere göre eşit',
    const Term(2026, Semester.fall) == const Term(2026, Semester.fall),
  );
  kontrol('Not harfi doğru', Grade.of(88).letter == 'BA');
  kontrol('Not katsayısı doğru', Grade.of(88).points == 3.5);
  kontrol(
    '49 kalır, 50 geçer',
    !Grade.of(49).isPassing && Grade.of(50).isPassing,
  );

  beklenenHata<FormatException>(
    'Geçersiz ders kodu reddedilir',
    () => CourseCode.parse('MATEMATIK'),
  );
  beklenenHata<FormatException>(
    'Geçersiz öğrenci no reddedilir',
    () =>
        Student.register(studentId: '12345', fullName: 'Test', startedIn: _guz),
  );
  beklenenHata<ArgumentError>('Geçersiz not reddedilir', () => Grade.of(150));

  print('  --- Kapsülleme ---');
  beklenenHata<UnsupportedError>(
    'Kayıt listesine dışarıdan ekleme engellenir',
    () => ayse.enrollments.add(ayse.enrollments.first),
  );
  beklenenHata<UnsupportedError>(
    'Ders listesi temizlenemez',
    () => mat101.roster.clear(),
  );

  print('  --- Invariant\'lar ---');
  {
    final t = Course.open(
      code: 'TST101',
      title: 'Test Dersi',
      credits: 3,
      capacity: 2,
    );
    final r = Registrar(policy: standardPolicy(), term: _guz);
    final a = Student.register(
      studentId: 'S-2026-9001',
      fullName: 'Test Bir',
      startedIn: _guz,
    );
    final b = Student.register(
      studentId: 'S-2026-9002',
      fullName: 'Test İki',
      startedIn: _guz,
    );
    final c = Student.register(
      studentId: 'S-2026-9003',
      fullName: 'Test Üç',
      startedIn: _guz,
    );

    final e1 = r.enroll(a, t);
    r.enroll(b, t);
    kontrol('C1: kontenjan doldu', t.isFull);
    kontrol(
      'C2: koltuk = aktif kayıt',
      t.seatsTaken == t.activeEnrollments.length,
    );

    beklenenHata<EnrollmentDenied>(
      'Dolu derse kayıt reddedilir',
      () => r.enroll(c, t),
    );

    kontrol('S1: aktif kredi doğru', a.activeCredits == 3);
    e1.drop();
    kontrol('Çekilince kredi düştü', a.activeCredits == 0);
    kontrol('Çekilince koltuk boşaldı', t.seatsLeft == 1);
    kontrol('Kayıt geçmişte kaldı', a.enrollments.length == 1);

    beklenenHata<StateError>('İkinci kez çekilinemez', () => e1.drop());
    beklenenHata<StateError>(
      'Çekilmiş kayıt notlandırılamaz',
      () => e1.complete(Grade.of(80)),
    );

    // Çekilen öğrenci yerine yenisi girebilir
    r.enroll(c, t);
    kontrol('Boşalan koltuğa yeni kayıt', t.isFull);
  }

  print('  --- Politikalar ---');
  {
    final r = Registrar(policy: standardPolicy(maxCredits: 12), term: _guz);
    final s = Student.register(
      studentId: 'S-2026-9010',
      fullName: 'Kredi Testi',
      startedIn: _guz,
    );

    r.enroll(s, prog101); // 8
    final karar = r.canEnroll(s, mat101); // +6 = 14 > 12
    kontrol('Kredi sınırı uygulanıyor', !karar.allowed);
    kontrol('Red sebebi açıklanıyor', karar.reason!.contains('Kredi sınırı'));

    final az = r.canEnroll(s, ing101); // +4 = 12
    kontrol('Sınıra tam oturan kayıt kabul edilir', az.allowed);
  }

  print('  --- Transkript ---');
  {
    final t = Transcript(ayse.enrollments);
    kontrol('Tamamlanan ders sayısı', t.completedCourses == 4);
    kontrol(
      'Kalınan ders krediye sayılmaz',
      t.earnedCredits == 19 && t.attemptedCredits == 23,
      'kazanılan ${t.earnedCredits}, denenen ${t.attemptedCredits}',
    );
    kontrol('GNO 2.0 üstü', t.gpa > 2.0);
    kontrol(
      'Boş transkript şartlı sayılmaz',
      Transcript(const []).isInGoodStanding,
    );
  }
  print('');

  // ==========================================================================
  print('=== BÖLÜM 7: HANGİ GÜN NEREDE ===');

  const harita = [
    ['1-2', 'Class/instance', 'Student, Course, Enrollment ayrı nesneler'],
    ['3', 'Constructor + validation', 'Course.open, Student.register'],
    ['4', 'Sorumluluk + command/query', 'canEnroll (query) / enroll (command)'],
    ['5', 'Domain model', 'Üç varlık, net sınırlar'],
    ['6', 'Encapsulation', 'Tüm alanlar private, salt okunur görünümler'],
    ['7', 'Invariant + fail fast', 'C1-C2, S1-S2, E1-E2; önce kontrol'],
    ['8', 'Abstraction', 'EnrollmentPolicy sözleşmesi'],
    ['9', 'Value vs entity', 'StudentId/Grade değer, Student varlık'],
    ['10', 'Hardening', 'Primitive obsession yok, enum ile durum'],
    ['11', 'Kalıtım', 'Kasten YOK — hiyerarşi kurmaya gerek olmadı'],
    ['12', 'Polymorphism', 'AllOfPolicy karışık politikalarda gezinir'],
    ['13', 'Interface + DI', 'Registrar politikayı dışarıdan alır'],
    ['14', 'Composition', 'AllOfPolicy sarmalayıcı; Transcript has-a'],
    ['15', 'Sığ yapı', 'Hiçbir yerde 2. seviye yok'],
    ['16', 'SRP + OCP', 'Transcript ayrı; yeni kural = yeni sınıf'],
    ['19', 'Küçük adımlar', 'Testler her ekleme sonrası çalıştırıldı'],
  ];

  print('  ${'GÜN'.padRight(6)}${'KAVRAM'.padRight(28)}NEREDE');
  print('  ${'-' * 6}${'-' * 28}${'-' * 44}');
  for (final h in harita) {
    print('  ${h[0].padRight(6)}${h[1].padRight(28)}${h[2]}');
  }

  print('');
  print('  Gün 11 satırına dikkat: bu modelde HİÇ kalıtım yok.');
  print('  Kalıtım bir hedef değil bir araçtır; ihtiyaç doğmadığı için');
  print('  kullanılmadı. Sözleşme + kompozisyon yetti.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 8: BİLİNÇLİ SINIRLAR ===');

  const sinirlar = [
    ['Kalıcılık yok', 'Repository sözleşmesi eklemek 20 satırlık iş'],
    ['Bekleme listesi yok', 'Gerçek ihtiyaç doğmadan tasarlamadım'],
    ['Observer yok', 'Bildirilecek kimse yoktu; desen için desen olmaz'],
    ['Term geçişi otomatik değil', 'Zamanı modele sokmak ayrı bir konu'],
    [
      'Politikalar tek tek çalışıyor',
      'Hepsini raporlamak istersen AllOf\'u değiştir',
    ],
  ];

  print('  ${'SINIR'.padRight(32)}GEREKÇE');
  print('  ${'-' * 32}${'-' * 46}');
  for (final s in sinirlar) {
    print('  ${s[0].padRight(32)}${s[1]}');
  }

  print('');
  print('  Bir tasarımın olgunluğu neyi içerdiğiyle değil, neyi');
  print('  BİLEREK dışarıda bıraktığıyla da ölçülür.');
  print('');

  // ==========================================================================
  print('=== SONUÇ ===');
  print('  Geçen: $_gecen');
  print('  Kalan: $_kalan');
  print(
    _kalan == 0
        ? '  Model tutarlı: geçersiz durum üretilemiyor, genişletme'
              ' mevcut kodu bozmuyor.'
        : '  DİKKAT: $_kalan kontrol başarısız!',
  );
}
