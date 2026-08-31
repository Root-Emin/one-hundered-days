// ============================================================================
// GÜN 19 — Observer Pattern & Legacy Refactoring  (Dart)
//
// Çalıştırmak için:  dart run gun19_observer_ve_legacy_refactor.dart
// veya dartpad.dev'e yapıştır.
//
// BÖLÜM 1 -> Observer Lite     : abonelik/bildirim akışı (subscribe/notify)
// BÖLÜM 2 -> Characterization  : legacy god-class + mevcut davranışı belgeleyen
//                                testler (DEĞİŞTİRMEDEN ÖNCE yazılır)
// BÖLÜM 3 -> Legacy Refactor   : god-class'tan 3 nesne çıkarmak
// BÖLÜM 4 -> Small Steps       : her adımdan sonra aynı testleri koşturmak
//
// NOT: 'abstract interface class' Dart 3 sözdizimidir. SDK'n eskiyse sadece
// 'abstract class' yaz; bu dosya için anlam aynı kalır.
// ============================================================================

// ============================================================================
// BÖLÜM 1 — OBSERVER LITE
//
// Problem: bir ödev yayınlandığında BİRDEN FAZLA yer tepki vermeli
// (öğrenci kutusu, veliye SMS, denetim kaydı...). Yayınlayan tarafın bunların
// hepsini tek tek çağırması demek, her yeni tepki için o sınıfı açmak demek
// (Gün 16 -> OCP ihlali).
//
// Observer çözümü: yayınlayan (Subject) sadece "bir şey oldu" der.
// Kimin dinlediğini bilmez, sadece SÖZLEŞMEYİ bilir.
//
// Üç parça vardır:
//   1) Event  -> ne olduğunu taşıyan veri            (Homework)
//   2) Observer sözleşmesi -> dinleyicinin şekli      (HomeworkObserver)
//   3) Subject -> abone listesi tutar ve haber verir  (HomeworkPublisher)
// ============================================================================

/// Olayı taşıyan basit, değişmez veri (Gün 9 -> immutable).
class Homework {
  final String id;
  final String title;
  final String classId;

  const Homework({
    required this.id,
    required this.title,
    required this.classId,
  });

  @override
  String toString() => '$title ($classId)';
}

/// GÖZLEMCİ SÖZLEŞMESİ.
/// Publisher'ın bildiği tek tip budur. StudentInbox mı, SMS mi, log mu —
/// umurunda değil (Gün 13 -> interface/contract, Gün 17 -> DIP).
abstract interface class HomeworkObserver {
  String get name;
  void onHomeworkPublished(Homework hw);
}

/// SUBJECT (yayınlayan taraf).
/// Sorumluluğu tek: abone listesini yönetmek ve olayı dağıtmak.
class HomeworkPublisher {
  final List<HomeworkObserver> _observers = <HomeworkObserver>[];

  int get observerCount => _observers.length;

  void subscribe(HomeworkObserver observer) {
    // Aynı gözlemciyi iki kez eklemek = her olayda çift bildirim.
    // Bu, Observer'ın en sık görülen hatasıdır; baştan engelliyoruz.
    if (_observers.contains(observer)) {
      print('  [publisher] ${observer.name} zaten abone, tekrar eklenmedi.');
      return;
    }
    _observers.add(observer);
    print('  [publisher] ${observer.name} abone oldu.');
  }

  /// Aboneliği bitirmek MUTLAKA mümkün olmalı. Flutter'da bunu unutmak
  /// = dispose edilmiş widget'a setState çağırmak = memory leak.
  void unsubscribe(HomeworkObserver observer) {
    if (_observers.remove(observer)) {
      print('  [publisher] ${observer.name} abonelikten çıktı.');
    }
  }

  void publish(Homework hw) {
    print('  [publisher] Yayın: $hw  (${_observers.length} abone)');

    // Listenin KOPYASI üzerinde dolaşıyoruz. Sebep: bir gözlemci callback'in
    // içinde unsubscribe() çağırırsa, orijinal liste dolaşırken değişir ve
    // Dart ConcurrentModificationError fırlatır.
    for (final observer in List<HomeworkObserver>.of(_observers)) {
      try {
        observer.onHomeworkPublished(hw);
      } on Object catch (e) {
        // Bir abonenin patlaması diğerlerini engellememeli.
        // Subject, abonelerin kalitesinden sorumlu değildir; dağıtımdan sorumludur.
        print('  [publisher] ${observer.name} hata verdi, yayın sürüyor -> $e');
      }
    }
  }
}

// ------------------------------------------------------------- Gözlemciler

class StudentInbox implements HomeworkObserver {
  final String studentName;
  final List<String> messages = <String>[];

  StudentInbox(this.studentName);

  @override
  String get name => 'StudentInbox($studentName)';

  @override
  void onHomeworkPublished(Homework hw) {
    messages.add('Yeni ödev: ${hw.title}');
    print('    -> $studentName kutusuna düştü (${messages.length} mesaj)');
  }
}

class ParentSmsNotifier implements HomeworkObserver {
  final String parentPhone;
  final List<String> sent = <String>[];

  ParentSmsNotifier(this.parentPhone);

  @override
  String get name => 'ParentSmsNotifier($parentPhone)';

  @override
  void onHomeworkPublished(Homework hw) {
    sent.add(hw.id);
    print('    -> $parentPhone numarasına SMS: "${hw.title}" verildi');
  }
}

class AuditLog implements HomeworkObserver {
  final List<String> rows = <String>[];

  @override
  String get name => 'AuditLog';

  @override
  void onHomeworkPublished(Homework hw) {
    rows.add('published:${hw.id}');
    print('    -> denetim kaydı yazıldı (${rows.length} satır)');
  }
}

/// Kasıtlı olarak bozuk gözlemci: hata izolasyonunu göstermek için.
class BuggyAnalytics implements HomeworkObserver {
  @override
  String get name => 'BuggyAnalytics';

  @override
  void onHomeworkPublished(Homework hw) {
    throw StateError('analitik servisi kapalı');
  }
}

// ============================================================================
// BÖLÜM 2 — CHARACTERIZATION (önce belgele, sonra dokun)
//
// Aşağıdaki sınıf tipik bir GOD CLASS: tek metotta parse + doğrulama +
// saklama + hesaplama + bildirim var. Kötü yazılmış ama ÇALIŞIYOR ve
// bir yerlerde kullanılıyor.
//
// Legacy koda dokunmadan önceki tek doğru hamle:
//   "Bu kod bugün ne yapıyor?" sorusunun cevabını TESTE çevirmek.
//
// Characterization test, "olması gerekeni" değil "OLANI" yazar.
// Tuhaflıkları bile aynen kaydeder — çünkü amaç doğruyu tarif etmek değil,
// refactor sırasında bir şeyin sessizce değişmediğinden emin olmaktır.
// ============================================================================

/// ADIM 0 (ilk küçük adım): ARAYÜZ ÇIKARMAK.
/// Henüz hiçbir davranış değişmedi. Sadece "not servisi" diye bir şekil
/// tanımladık. Bu şekil sayesinde AYNI testleri hem eski hem yeni koda
/// koşturabileceğiz — refactor'un emniyet kemeri budur (Gün 17 -> DIP).
abstract interface class GradeService {
  String addGrade(String studentId, String rawGrade);
  String report(String studentId);
  List<String> get log;
}

/// LEGACY GOD CLASS — dokunmadan önceki hâli.
/// addGrade() tek başına 5 farklı iş yapıyor. Her biri ayrı bir değişme
/// sebebi = 5 ayrı SRP ihlali (Gün 16).
class LegacyGradeManager implements GradeService {
  final Map<String, List<double>> _grades = <String, List<double>>{};
  final List<String> _log = <String>[];

  @override
  List<String> get log => _log;

  @override
  String addGrade(String studentId, String rawGrade) {
    // (1) metinden sayıya çevirme
    final value = double.tryParse(rawGrade);
    if (value == null) {
      _log.add('HATA: geçersiz not "$rawGrade"');
      return 'ERR_PARSE';
    }

    // (2) kural doğrulama
    if (value < 0 || value > 100) {
      _log.add('HATA: aralık dışı $value');
      return 'ERR_RANGE';
    }

    // (3) saklama
    _grades.putIfAbsent(studentId, () => <double>[]).add(value);

    // (4) hesaplama
    final list = _grades[studentId]!;
    var total = 0.0;
    for (final g in list) {
      total += g;
    }
    final avg = total / list.length;

    // (5) karar + bildirim
    if (avg < 50) {
      _log.add('SMS: $studentId ortalaması ${avg.toStringAsFixed(1)} - düşük');
    } else {
      _log.add('BİLGİ: $studentId ortalaması ${avg.toStringAsFixed(1)}');
    }

    return 'OK';
  }

  @override
  String report(String studentId) {
    final list = _grades[studentId];
    if (list == null) return '$studentId: kayıt yok';
    var total = 0.0;
    for (final g in list) {
      total += g;
    }
    final avg = total / list.length;
    return '$studentId | ${list.length} not | ort ${avg.toStringAsFixed(1)}';
  }
}

// ------------------------------------------------------- mini test koşucusu
// Paket kurmadan çalışsın diye elle yazılmış minik bir harness.
// Gerçek projede bunun yeri test/ klasörü ve package:test'tir.

class TestRunner {
  final String label;
  int _passed = 0;
  int _failed = 0;

  TestRunner(this.label);

  void expectEquals(String name, Object? actual, Object? expected) {
    if (actual == expected) {
      _passed++;
      print('    [gecti] $name');
    } else {
      _failed++;
      print('    [KALDI] $name  (beklenen: $expected | gelen: $actual)');
    }
  }

  void summary() {
    print('  >> $label : $_passed geçti, $_failed kaldı');
  }
}

/// CHARACTERIZATION SUITE.
/// Dikkat: parametre bir FABRİKA fonksiyonu (GradeService Function()).
/// Böylece her test taze bir nesneyle başlar (testler birbirini kirletmez)
/// ve aynı suite hem legacy hem refactor edilmiş sürüme koşturulabilir.
void characterizationSuite(String label, GradeService Function() make) {
  print('  --- $label ---');
  final t = TestRunner(label);

  // C1 — mutlu yol
  var s = make();
  t.expectEquals('C1 geçerli not "OK" döner', s.addGrade('ali', '80'), 'OK');

  // C2 — sayıya çevrilemeyen girdi
  s = make();
  t.expectEquals(
    'C2 bozuk metin ERR_PARSE',
    s.addGrade('ali', 'iyi'),
    'ERR_PARSE',
  );
  t.expectEquals('C2 log satırı', s.log.last, 'HATA: geçersiz not "iyi"');

  // C3 — aralık dışı
  s = make();
  t.expectEquals('C3 101 -> ERR_RANGE', s.addGrade('ali', '101'), 'ERR_RANGE');
  t.expectEquals('C3 log satırı', s.log.last, 'HATA: aralık dışı 101.0');

  // C4 — düşük ortalama bildirimi
  s = make();
  s.addGrade('ayse', '30');
  t.expectEquals(
    'C4 düşük ortalama SMS üretir',
    s.log.last,
    'SMS: ayse ortalaması 30.0 - düşük',
  );

  // C5 — SINIR DAVRANIŞI. Ortalama tam 50 iken SMS gitmiyor (kural: avg < 50).
  // Bu satır çok kıymetli: refactor'da yanlışlıkla '<=' yazarsan burası yakalar.
  s = make();
  s.addGrade('ayse', '40');
  s.addGrade('ayse', '60');
  t.expectEquals(
    'C5 ortalama tam 50 -> BİLGİ',
    s.log.last,
    'BİLGİ: ayse ortalaması 50.0',
  );

  // C6 — kaydı olmayan öğrenci
  s = make();
  t.expectEquals(
    'C6 bilinmeyen öğrenci raporu',
    s.report('yok'),
    'yok: kayıt yok',
  );

  // C7 — rapor metninin birebir formatı
  s = make();
  s.addGrade('mehmet', '90');
  s.addGrade('mehmet', '70');
  t.expectEquals(
    'C7 rapor formatı',
    s.report('mehmet'),
    'mehmet | 2 not | ort 80.0',
  );

  t.summary();
}

/// TUHAFLIK (quirk) TESTİ — ayrı tutuluyor.
/// Legacy kod boş öğrenci id'sini sessizce kabul ediyor. Bu bir HATA,
/// ama şu an gerçek davranış bu. Characterization aşamasında hatayı
/// düzeltmiyoruz; sadece kayda geçiriyoruz.
/// Ayrı bir fonksiyon olmasının sebebi: BÖLÜM 4'te bu davranışı bilerek
/// değiştireceğiz ve beklentiyi tek yerden güncelleyebilmemiz gerek.
void quirkTest(String label, GradeService service, String expected) {
  print('  --- $label ---');
  final t = TestRunner(label);
  t.expectEquals(
    'Q1 boş öğrenci id davranışı',
    service.addGrade('', '80'),
    expected,
  );
  t.summary();
}

// ============================================================================
// BÖLÜM 3 — LEGACY REFACTOR (god class'tan 3 nesne çıkarmak)
//
// God class'ın 5 işini gruplayıp SORUMLULUK SINIRLARI çiziyoruz:
//
//   (1) metin -> sayı + kural kontrolü ....... GradeParser     [nesne 1]
//   (3) saklama + (4) hesaplama .............. GradeBook       [nesne 2]
//   (5) karar + bildirim ..................... AverageBroadcaster + gözlemciler
//                                              (BÖLÜM 1'in aynısı)  [nesne 3]
//
// Geriye kalan GradeServiceV2 ise sadece SIRALAMA yapar: ayrıştır, sakla,
// haber ver. Buna "ince orkestratör" denir.
//
// Kritik nokta: hiçbir LOG METNİ değişmiyor. Refactoring tanımı gereği
// "dışarıdan görünen davranışı değiştirmeden iç yapıyı iyileştirmek"tir.
// ============================================================================

/// [nesne 1] Ayrıştırma + doğrulama sonucunu taşıyan küçük değer nesnesi.
/// Gün 4'teki "command/query" ayrımı: parse() bir SORU sorar, yan etkisi yok.
class GradeParseResult {
  final double? value;
  final String? errorCode;
  final String? errorLog;

  GradeParseResult.ok(this.value) : errorCode = null, errorLog = null;

  GradeParseResult.error(this.errorCode, this.errorLog) : value = null;

  bool get isOk => errorCode == null;
}

class GradeParser {
  GradeParseResult parse(String raw) {
    final v = double.tryParse(raw);
    if (v == null) {
      return GradeParseResult.error('ERR_PARSE', 'HATA: geçersiz not "$raw"');
    }
    if (v < 0 || v > 100) {
      return GradeParseResult.error('ERR_RANGE', 'HATA: aralık dışı $v');
    }
    return GradeParseResult.ok(v);
  }
}

/// [nesne 2] Notların deposu ve hesabı. Bildirimden, metinden, SMS'ten habersiz.
/// Yarın veriyi Firestore'a taşımak istersen sadece burayı değiştirirsin.
class GradeBook {
  final Map<String, List<double>> _grades = <String, List<double>>{};

  void add(String studentId, double grade) {
    _grades.putIfAbsent(studentId, () => <double>[]).add(grade);
  }

  bool has(String studentId) => _grades.containsKey(studentId);

  int countOf(String studentId) => _grades[studentId]?.length ?? 0;

  double averageOf(String studentId) {
    final list = _grades[studentId];
    if (list == null || list.isEmpty) return 0;
    var total = 0.0;
    for (final g in list) {
      total += g;
    }
    return total / list.length;
  }
}

/// [nesne 3] BÖLÜM 1'deki Observer, bu kez "ortalama değişti" olayı için.
abstract interface class AverageObserver {
  void onAverageChanged(String studentId, double average);
}

class LowAverageAlert implements AverageObserver {
  final List<String> _log;
  final double threshold;

  LowAverageAlert(this._log, {this.threshold = 50});

  @override
  void onAverageChanged(String studentId, double average) {
    if (average < threshold) {
      _log.add(
        'SMS: $studentId ortalaması ${average.toStringAsFixed(1)} - düşük',
      );
    }
  }
}

class AverageInfoLogger implements AverageObserver {
  final List<String> _log;
  final double threshold;

  AverageInfoLogger(this._log, {this.threshold = 50});

  @override
  void onAverageChanged(String studentId, double average) {
    if (average >= threshold) {
      _log.add('BİLGİ: $studentId ortalaması ${average.toStringAsFixed(1)}');
    }
  }
}

class AverageBroadcaster {
  final List<AverageObserver> _observers = <AverageObserver>[];

  void subscribe(AverageObserver o) => _observers.add(o);

  void notify(String studentId, double average) {
    for (final o in List<AverageObserver>.of(_observers)) {
      o.onAverageChanged(studentId, average);
    }
  }
}

/// REFACTOR EDİLMİŞ SERVİS.
/// addGrade() artık 5 iş yapmıyor; 3 nesneyi sırayla çağırıyor.
/// Yeni bir bildirim türü eklemek için bu sınıfa DOKUNMAK GEREKMİYOR —
/// broadcaster'a yeni bir gözlemci abone etmek yeterli (OCP).
class GradeServiceV2 implements GradeService {
  final GradeParser _parser = GradeParser();
  final GradeBook _book = GradeBook();
  final List<String> _log = <String>[];
  late final AverageBroadcaster _events;

  GradeServiceV2() {
    _events = AverageBroadcaster()
      ..subscribe(LowAverageAlert(_log))
      ..subscribe(AverageInfoLogger(_log));
  }

  @override
  List<String> get log => _log;

  @override
  String addGrade(String studentId, String rawGrade) {
    final result = _parser.parse(rawGrade);
    if (!result.isOk) {
      _log.add(result.errorLog!);
      return result.errorCode!;
    }
    _book.add(studentId, result.value!);
    _events.notify(studentId, _book.averageOf(studentId));
    return 'OK';
  }

  @override
  String report(String studentId) {
    if (!_book.has(studentId)) return '$studentId: kayıt yok';
    return '$studentId | ${_book.countOf(studentId)} not '
        '| ort ${_book.averageOf(studentId).toStringAsFixed(1)}';
  }
}

// ============================================================================
// BÖLÜM 4 — SMALL STEPS (küçük adımlar ve DAVRANIŞ DEĞİŞİKLİĞİ)
//
// Buraya kadar yapılan her şey "davranış sabit" kuralına uydu.
// Şimdi bilerek bir DAVRANIŞ DEĞİŞTİRİYORUZ: boş öğrenci id'si artık hata.
//
// Bunun ayrı bir adım olması şart. Refactoring ile davranış değişikliğini
// aynı commit'e koyarsan, test kırmızıya döndüğünde "yapıyı mı bozdum,
// kuralı mı değiştirdim?" sorusunu cevaplayamazsın.
//
// Kural: bir commit ya yapıyı değiştirir ya davranışı. İkisini birden asla.
// ============================================================================

class GradeServiceV3 extends GradeServiceV2 {
  @override
  String addGrade(String studentId, String rawGrade) {
    if (studentId.trim().isEmpty) {
      log.add('HATA: öğrenci id boş');
      return 'ERR_STUDENT';
    }
    // Geri kalan akış aynı; sadece kapıya bir bekçi koyduk (Gün 7 -> fail fast).
    return super.addGrade(studentId, rawGrade);
  }
}

// ============================================================================
// MAIN — hepsini çalıştır
// ============================================================================

void main() {
  print('===== BÖLÜM 1: OBSERVER LITE =====\n');

  final publisher = HomeworkPublisher();
  final ali = StudentInbox('Ali');
  final zeynep = StudentInbox('Zeynep');
  final veli = ParentSmsNotifier('+90555...');
  final audit = AuditLog();

  publisher.subscribe(ali);
  publisher.subscribe(zeynep);
  publisher.subscribe(veli);
  publisher.subscribe(audit);
  publisher.subscribe(ali); // çift abonelik denemesi -> engellenir

  print('');
  publisher.publish(
    const Homework(id: 'h1', title: 'Kesirler 1-20', classId: '7A'),
  );

  print('\n  Veli aboneliği bırakıyor, bozuk bir servis abone oluyor:');
  publisher.unsubscribe(veli);
  publisher.subscribe(BuggyAnalytics());

  print('');
  publisher.publish(
    const Homework(id: 'h2', title: 'Okuma raporu', classId: '7A'),
  );

  print(
    '\n  Sonuç: Ali\'nin kutusunda ${ali.messages.length} mesaj var, '
    'veliye toplam ${veli.sent.length} SMS gitti (2. yayını kaçırdı).',
  );
  print('  Publisher hâlâ ${publisher.observerCount} aboneye hizmet veriyor.');

  print('\n\n===== BÖLÜM 2: CHARACTERIZATION (legacy, dokunulmamış) =====\n');
  characterizationSuite('V1 legacy', () => LegacyGradeManager());
  quirkTest('V1 tuhaflık', LegacyGradeManager(), 'OK');

  print('\n\n===== BÖLÜM 3+4: REFACTOR SONRASI AYNI TESTLER =====\n');
  characterizationSuite('V2 refactor', () => GradeServiceV2());
  quirkTest('V2 tuhaflık (hâlâ korunuyor)', GradeServiceV2(), 'OK');

  print('\n  V2 tüm testleri geçti -> iç yapı değişti, davranış değişmedi.\n');

  print('===== BÖLÜM 4: BİLİNÇLİ DAVRANIŞ DEĞİŞİKLİĞİ =====\n');
  characterizationSuite('V3 (yapı testleri değişmedi)', () => GradeServiceV3());
  quirkTest(
    'V3 tuhaflık DÜZELTİLDİ (beklenti güncellendi)',
    GradeServiceV3(),
    'ERR_STUDENT',
  );

  print('\n  Not: V3\'te sadece Q1\'in beklentisi değişti. C1-C7 aynı kaldı —');
  print('  yani değişikliğin etki alanı tam olarak amaçladığımız kadar.');
}
