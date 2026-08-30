// ============================================================================
// GÜN 13 — INTERFACE VE ABSTRACT SÖZLEŞMELER  (Dart)
//
// Çalıştırmak için:  dart run gun13_interface_ve_sozlesme.dart
// veya dartpad.dev'e yapıştır.
//
// BÖLÜM 1 -> Sözleşme tanımlamak (saf interface)
// BÖLÜM 2 -> İki farklı implementasyon
// BÖLÜM 3 -> Sözleşmeye bağlı çağıran kod
// BÖLÜM 4 -> Implementasyonları değiştirmek
// BÖLÜM 5 -> Asıl kazanç: test edilebilirlik
// BÖLÜM 6 -> Abstract class: ortak kod içeren sözleşme
// BÖLÜM 7 -> Interface mi abstract class mı? Dart 3 modifier'ları
// ============================================================================

// ============================================================================
// VERİ NESNESİ
// ============================================================================

class Student {
  final String id;
  final String fullName;
  final String classroom;
  final int average;

  const Student({
    required this.id,
    required this.fullName,
    required this.classroom,
    this.average = 0,
  });

  Student copyWith({String? classroom, int? average}) => Student(
    id: id,
    fullName: fullName,
    classroom: classroom ?? this.classroom,
    average: average ?? this.average,
  );

  @override
  String toString() => '$fullName ($classroom, ort. $average)';
}

class RepositoryException implements Exception {
  final String message;
  const RepositoryException(this.message);
  @override
  String toString() => 'RepositoryException: $message';
}

// ############################################################################
//
//  BÖLÜM 1 — SÖZLEŞMEYİ TANIMLAMAK
//
//  Bir sözleşme NE yapılabileceğini söyler, NASIL yapıldığını söylemez.
//  Aşağıda hiçbir gövde yok — sadece imzalar.
//
//  Dikkat: burada "Firestore", "SQL", "cache", "HTTP" gibi hiçbir kelime
//  geçmiyor. Sözleşmeye implementasyon detayı sızarsa (örneğin
//  'DocumentSnapshot' döndürseydi) artık soyut olmazdı ve ikinci bir
//  implementasyon yazmak imkânsızlaşırdı.
//
//  SÖZLEŞMEYİ İKİYE BÖLDÜK — neden:
//  Sadece okuma yapan bir ekran, silme metodunu görmemeli. Küçük ve
//  odaklı sözleşmeler, çağıranın ihtiyacı olmayan yetkiyi almasını
//  engeller. (Bu fikir Interface Segregation Principle olarak
//  karşına çıkacak.)
//
// ############################################################################

abstract interface class StudentReader {
  Future<Student?> findById(String id);
  Future<List<Student>> findByClassroom(String classroom);
  Future<int> count();
}

abstract interface class StudentWriter {
  Future<void> save(Student student);
  Future<void> delete(String id);
}

/// Bir sınıf tek bir sınıftan EXTENDS edebilir ama istediği kadar
/// interface'i IMPLEMENTS edebilir. Kalıtımın yapamadığı şey bu.
abstract interface class StudentRepository
    implements StudentReader, StudentWriter {}

// ############################################################################
//
//  BÖLÜM 2 — İKİ FARKLI IMPLEMENTASYON
//
//  Aynı sözleşme, tamamen farklı iki iç dünya.
//
// ############################################################################

/// IMPLEMENTASYON 1: Bellekte tutan sürüm.
/// Anında cevap verir, ağ gerektirmez, testlerde kullanılır.
class InMemoryStudentRepository implements StudentRepository {
  final Map<String, Student> _store = {};

  /// Sözleşmede olmayan, sadece teste yarayan yardımcı.
  /// Interface'e koymadık; oraya koysaydık gerçek veritabanı sürümü de
  /// bunu yazmak zorunda kalırdı ve sözleşme kirlenirdi.
  void seed(List<Student> students) {
    for (final s in students) {
      _store[s.id] = s;
    }
  }

  @override
  Future<Student?> findById(String id) async => _store[id];

  @override
  Future<List<Student>> findByClassroom(String classroom) async =>
      _store.values.where((s) => s.classroom == classroom).toList();

  @override
  Future<int> count() async => _store.length;

  @override
  Future<void> save(Student student) async {
    _store[student.id] = student;
  }

  @override
  Future<void> delete(String id) async {
    if (!_store.containsKey(id)) {
      throw RepositoryException('Öğrenci bulunamadı: $id');
    }
    _store.remove(id);
  }
}

/// IMPLEMENTASYON 2: Uzak sunucuyu taklit eden sürüm.
/// Gecikmeli çalışır, çağrıları kaydeder, istenirse hata fırlatır.
/// (Gerçek projede burası Firestore/HTTP çağrıları olurdu.)
class RemoteStudentRepository implements StudentRepository {
  final Map<String, Student> _serverData = {};
  final List<String> callLog = [];
  final Duration latency;

  bool simulateOutage = false;

  RemoteStudentRepository({this.latency = const Duration(milliseconds: 30)});

  void seed(List<Student> students) {
    for (final s in students) {
      _serverData[s.id] = s;
    }
  }

  Future<void> _networkCall(String operation) async {
    callLog.add(operation);
    await Future.delayed(latency);
    if (simulateOutage) {
      throw const RepositoryException('Sunucuya ulaşılamıyor');
    }
  }

  @override
  Future<Student?> findById(String id) async {
    await _networkCall('GET /students/$id');
    return _serverData[id];
  }

  @override
  Future<List<Student>> findByClassroom(String classroom) async {
    await _networkCall('GET /students?classroom=$classroom');
    return _serverData.values.where((s) => s.classroom == classroom).toList();
  }

  @override
  Future<int> count() async {
    await _networkCall('GET /students/count');
    return _serverData.length;
  }

  @override
  Future<void> save(Student student) async {
    await _networkCall('PUT /students/${student.id}');
    _serverData[student.id] = student;
  }

  @override
  Future<void> delete(String id) async {
    await _networkCall('DELETE /students/$id');
    if (!_serverData.containsKey(id)) {
      throw RepositoryException('Öğrenci bulunamadı: $id');
    }
    _serverData.remove(id);
  }
}

// ############################################################################
//
//  BÖLÜM 3 — SÖZLEŞMEYE BAĞLI ÇAĞIRAN KOD
//
//  Bu sınıfın tipi StudentRepository. Hangi implementasyonla çalıştığını
//  BİLMİYOR ve bilmesine gerek yok.
//
// ############################################################################

class StudentDirectory {
  final StudentRepository _repository; // somut sınıf değil, SÖZLEŞME

  const StudentDirectory(this._repository);

  Future<String> classRoster(String classroom) async {
    final students = await _repository.findByClassroom(classroom);
    if (students.isEmpty) return '$classroom sınıfında kayıtlı öğrenci yok.';

    students.sort((a, b) => a.fullName.compareTo(b.fullName));
    final satirlar = students.map((s) => '    - ${s.fullName}').join('\n');
    return '$classroom (${students.length} öğrenci):\n$satirlar';
  }

  Future<void> enroll(Student student) async {
    final mevcut = await _repository.findById(student.id);
    if (mevcut != null) {
      throw RepositoryException('${student.id} zaten kayıtlı');
    }
    await _repository.save(student);
  }

  Future<void> transfer(String studentId, String newClassroom) async {
    final student = await _repository.findById(studentId);
    if (student == null) {
      throw RepositoryException('Öğrenci bulunamadı: $studentId');
    }
    await _repository.save(student.copyWith(classroom: newClassroom));
  }

  Future<double> classAverage(String classroom) async {
    final students = await _repository.findByClassroom(classroom);
    if (students.isEmpty) return 0;
    final toplam = students.map((s) => s.average).reduce((a, b) => a + b);
    return toplam / students.length;
  }
}

/// SADECE OKUMA yeten fonksiyon. Parametre tipi StudentReader.
/// StudentWriter'ı hiç görmüyor, dolayısıyla yanlışlıkla silme
/// yapması MÜMKÜN DEĞİL. Sözleşmeyi küçük tutmanın somut faydası.
Future<String> ozetCikar(StudentReader reader, String classroom) async {
  final adet = await reader.count();
  final sinif = await reader.findByClassroom(classroom);
  return 'Toplam $adet öğrenci, $classroom sınıfında ${sinif.length} kişi';
  // reader.delete('S-1');  <-- DERLENMEZ: StudentReader'da delete yok
}

// ############################################################################
//
//  KÖTÜ ÖRNEK — BAĞIMLILIĞI KENDİ İÇİNDE ÜRETMEK
//
//  Bu sınıf hangi depoyu kullanacağına KENDİ karar veriyor.
//  Sonuçları:
//    - Testte gerçek sunucuya bağlanmak zorunda kalırsın
//    - Bellekteki sürüme geçmek için bu dosyayı değiştirmen gerekir
//    - Aynı sınıfı iki farklı yapılandırmayla kullanamazsın
//
// ############################################################################

class BozukStudentDirectory {
  // Bağımlılık dışarıdan verilmiyor, içeride üretiliyor.
  final RemoteStudentRepository _repository = RemoteStudentRepository();

  Future<int> count() => _repository.count();
}

// ############################################################################
//
//  BÖLÜM 6 — ABSTRACT CLASS: ORTAK KOD İÇEREN SÖZLEŞME
//
//  StudentRepository saf bir interface'ti: hiç kod yoktu, çünkü bellek
//  ve sunucu sürümlerinin paylaşacağı ortak mantık yoktu.
//
//  Rapor üreticilerde durum farklı: hepsi "başlık + satırlar + alt bilgi"
//  sırasını izliyor. Bu ORTAK KOD var, tekrar yazılmamalı.
//
//  Kural: paylaşılacak kod varsa abstract class, yoksa interface.
//
// ############################################################################

abstract class ReportGenerator {
  /// TEMPLATE METHOD — ortak kod. Alt sınıflar bunu override etmiyor.
  String generate(List<Student> students) {
    final buffer = StringBuffer();
    buffer.writeln(header());
    for (final student in students) {
      buffer.writeln(row(student));
    }
    buffer.write(footer(students));
    return buffer.toString();
  }

  // Alt sınıfların doldurması gereken boşluklar:
  String header();
  String row(Student student);

  /// Ortak VARSAYILAN davranış. İsteyen override eder, istemeyen kullanır.
  /// Saf interface'te bu mümkün olmazdı; herkes yazmak zorunda kalırdı.
  String footer(List<Student> students) => 'Toplam: ${students.length} öğrenci';
}

class CsvReportGenerator extends ReportGenerator {
  @override
  String header() => 'id,ad,sinif,ortalama';

  @override
  String row(Student s) => '${s.id},${s.fullName},${s.classroom},${s.average}';

  /// CSV'de alt bilgi satırı veriyi bozar; boş geçiyoruz.
  @override
  String footer(List<Student> students) => '';
}

class TextReportGenerator extends ReportGenerator {
  @override
  String header() => '── SINIF LİSTESİ ──────────────────';

  @override
  String row(Student s) =>
      '  ${s.fullName.padRight(18)} ${s.classroom.padRight(6)} ${s.average}';

  // footer override edilmedi: base'in varsayılanı yeterli.
}

// ############################################################################
//  MİNİ TEST ALTYAPISI
// ############################################################################

int _gecen = 0;
int _kalan = 0;

void kontrol(String etiket, bool kosul) {
  if (kosul) {
    _gecen++;
    print('    ✓ $etiket');
  } else {
    _kalan++;
    print('    ✗ $etiket  <-- BAŞARISIZ');
  }
}

Future<void> beklenenHata(String etiket, Future<void> Function() eylem) async {
  try {
    await eylem();
    _kalan++;
    print('    ✗ $etiket  <-- hata bekleniyordu');
  } on RepositoryException {
    _gecen++;
    print('    ✓ $etiket');
  }
}

// ============================================================================
// ÇALIŞTIRMA
// ============================================================================

const _ogrenciler = [
  Student(id: 'S-1', fullName: 'Mehmet Demir', classroom: '9-A', average: 78),
  Student(id: 'S-2', fullName: 'Ayşe Kaya', classroom: '9-A', average: 91),
  Student(id: 'S-3', fullName: 'Can Yıldız', classroom: '9-B', average: 64),
  Student(id: 'S-4', fullName: 'Elif Şahin', classroom: '9-A', average: 85),
];

Future<void> main() async {
  // ==========================================================================
  print('=== BÖLÜM 4: AYNI ÇAĞIRAN, İKİ FARKLI DEPO ===');

  final bellek = InMemoryStudentRepository()..seed(_ogrenciler);
  final uzak = RemoteStudentRepository()..seed(_ogrenciler);

  // AYNI sınıf, farklı constructor argümanı. StudentDirectory kodunda
  // tek satır değişmiyor.
  final depolar = <String, StudentRepository>{
    'Bellek': bellek,
    'Uzak sunucu': uzak,
  };

  for (final entry in depolar.entries) {
    final baslangic = DateTime.now();
    final directory = StudentDirectory(entry.value);

    final liste = await directory.classRoster('9-A');
    final ortalama = await directory.classAverage('9-A');
    final sure = DateTime.now().difference(baslangic).inMilliseconds;

    print('  [${entry.key}]  (${sure}ms)');
    print('  $liste');
    print('    ortalama: ${ortalama.toStringAsFixed(1)}');
    print('');
  }

  print('  Sonuçlar aynı, süreler farklı. Çağıran kod ikisini de');
  print('  ayırt edemiyor — sözleşmeye bakıyor, sınıfa değil.');
  print('');

  print('  Uzak deponun yaptığı ağ çağrıları:');
  for (final c in uzak.callLog) {
    print('    $c');
  }
  print('  Bellek sürümü hiç ağ çağrısı yapmadı.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 5: ASIL KAZANÇ — TEST EDİLEBİLİRLİK ===');
  print(
    '  StudentDirectory\'yi test ediyoruz. Sunucu yok, ağ yok, saniye yok.',
  );
  print('');

  {
    final testDepo = InMemoryStudentRepository()..seed(_ogrenciler);
    final directory = StudentDirectory(testDepo);

    print('  --- Kayıt (enroll) ---');
    await directory.enroll(
      const Student(
        id: 'S-5',
        fullName: 'Zeynep Ak',
        classroom: '9-B',
        average: 70,
      ),
    );
    kontrol('Yeni öğrenci eklendi', await testDepo.count() == 5);

    await beklenenHata(
      'Aynı id ile ikinci kayıt reddedilir',
      () => directory.enroll(
        const Student(id: 'S-5', fullName: 'Kopya', classroom: '9-C'),
      ),
    );

    print('  --- Nakil (transfer) ---');
    await directory.transfer('S-1', '9-C');
    final nakledilen = await testDepo.findById('S-1');
    kontrol('Sınıf değişti', nakledilen?.classroom == '9-C');
    kontrol('İsim korundu', nakledilen?.fullName == 'Mehmet Demir');
    kontrol(
      '9-A artık 2 kişi',
      (await testDepo.findByClassroom('9-A')).length == 2,
    );

    await beklenenHata(
      'Olmayan öğrenci nakledilemez',
      () => directory.transfer('S-999', '9-C'),
    );

    print('  --- Sınıf listesi ---');
    final bosListe = await directory.classRoster('12-Z');
    kontrol('Boş sınıf düzgün mesaj veriyor', bosListe.contains('kayıtlı'));
  }

  print('');
  print('  Bu testler milisaniyeler içinde çalıştı.');
  print('  Gerçek Firestore ile: her test ağ bekler, kota harcar,');
  print('  paralel çalışamaz, internet yoksa hiç çalışmaz ve');
  print('  test verisi gerçek veritabanına karışır.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 5.1: HATA SENARYOSUNU TAKLİT ETMEK ===');

  uzak.simulateOutage = true;
  try {
    await StudentDirectory(uzak).classRoster('9-A');
    print('  BEKLENMEDİK: hata fırlamadı');
  } on RepositoryException catch (e) {
    print('  Sunucu kesintisi taklit edildi: ${e.message}');
    _gecen++;
  }
  uzak.simulateOutage = false;

  print('  Sözleşme sayesinde "sunucu çöktüğünde ne olur" senaryosunu');
  print('  sunucuyu çökertmeden test edebiliyoruz.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 3.1: KÜÇÜK SÖZLEŞME, DAR YETKİ ===');

  print('  ${await ozetCikar(bellek, '9-A')}');
  print('  ozetCikar() parametresi StudentReader. delete() ve save()');
  print('  o tipte yok, dolayısıyla bu fonksiyon veri silemez —');
  print('  disiplinle değil, DERLEYİCİYLE garanti altında.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 6: ABSTRACT CLASS ILE ORTAK KOD ===');

  final ogrenciler9A = await bellek.findByClassroom('9-A');

  final ureticiler = <String, ReportGenerator>{
    'Metin': TextReportGenerator(),
    'CSV': CsvReportGenerator(),
  };

  for (final entry in ureticiler.entries) {
    print('  --- ${entry.key} ---');
    print(entry.value.generate(ogrenciler9A));
    print('');
  }

  print('  generate() ikisinde de yazılmadı; base\'den geldi.');
  print('  Sıra (başlık→satırlar→alt bilgi) tek yerde tanımlı.');
  print('  CsvReportGenerator footer\'ı override etti (CSV\'de alt bilgi');
  print('  veriyi bozar), TextReportGenerator varsayılanı kullandı.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 7: HANGİSİNİ SEÇMELİ? ===');

  const karar = [
    ['Paylaşılacak ortak kod var mı?', 'Evet -> abstract class'],
    ['Sadece sözleşme mi kuruyorsun?', 'Evet -> interface'],
    ['Birden fazla sözleşme gerekli mi?', 'Evet -> interface (çoklu)'],
    ['Alt sınıflar aynı aileden mi?', 'Evet -> abstract class'],
    ['Farklı ailelerden sınıflar mı uyacak?', 'Evet -> interface'],
    ['Varsayılan davranış istiyor musun?', 'Evet -> abstract class'],
  ];

  for (final k in karar) {
    print('  ${k[0].padRight(40)}${k[1]}');
  }

  print('');
  print('  DART 3 SINIF MODIFIER\'LARI:');
  const modifiers = [
    ['abstract class', 'nesne üretilemez; extends ve implements serbest'],
    ['interface class', 'implements edilebilir, extends EDİLEMEZ'],
    ['abstract interface class', 'saf sözleşme — bugünkü StudentReader'],
    ['base class', 'extends edilebilir, implements EDİLEMEZ'],
    ['final class', 'ne extends ne implements — kapalı'],
    ['sealed class', 'alt tipleri aynı dosyada; switch exhaustive olur'],
    ['mixin', 'with ile eklenir; çoklu davranış paylaşımı'],
  ];
  for (final m in modifiers) {
    print('    ${m[0].padRight(28)}${m[1]}');
  }

  print('');
  print('  Dart\'ta HER sınıf zaten örtük bir interface tanımlar.');
  print('  \'implements InMemoryStudentRepository\' yazmak bile geçerlidir');
  print('  ama kötü fikirdir — somut bir sınıfın imzalarına bağlanmış');
  print('  olursun. Sözleşmeyi ayrı ve niyetli olarak tanımla.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 8: DEPENDENCY INVERSION ===');

  print('  ÖNCE (BozukStudentDirectory):');
  print('    StudentDirectory ──► RemoteStudentRepository');
  print('    Üst seviye kod, alt seviye detaya bağımlı.');
  print('    Depoyu değiştirmek = bu sınıfı değiştirmek.');
  print('');
  print('  SONRA (StudentDirectory):');
  print('    StudentDirectory ──► StudentRepository ◄── InMemory...');
  print('                                           ◄── Remote...');
  print('    İkisi de SOYUTA bakıyor. Ok yönü tersine döndü.');
  print('');
  print('  Kural iki cümle:');
  print('    1. Üst seviye modüller alt seviye modüllere bağımlı olmasın;');
  print('       ikisi de soyutlamalara bağımlı olsun.');
  print('    2. Soyutlamalar detaylara değil, detaylar soyutlamalara');
  print('       uysun.');
  print('');
  print('  Pratik işareti: bir sınıf içinde \'new\'/constructor çağrısıyla');
  print('  kendi bağımlılığını üretiyorsa, o bağımlılık çivilenmiştir.');
  print('  Constructor parametresine taşı — yaptığın şey bu kadar basit.');
  print('  (Flutter\'da Provider, Riverpod, get_it bunu otomatikleştirir.)');
  print('');

  print('=== SONUÇ ===');
  print('  Geçen: $_gecen');
  print('  Kalan: $_kalan');
  print(
    _kalan == 0
        ? '  Aynı çağıran kod, iki farklı dünyada sorunsuz çalıştı.'
        : '  DİKKAT: $_kalan kontrol başarısız!',
  );
}
