// ============================================================================
// GÜN 11 — INHERITANCE (KALITIM) TEMELLERİ  (Dart)
//
// Çalıştırmak için:  dart run gun11_inheritance.dart
// veya dartpad.dev'e yapıştır.
//
// BÖLÜM 1 -> Base class: ortak alanlar ve davranışlar
// BÖLÜM 2 -> İki alt sınıf: Student ve Teacher
// BÖLÜM 3 -> Miras alınan davranışı kullanmak
// BÖLÜM 4 -> Constructor sırası ve super
// BÖLÜM 5 -> extends vs implements
// BÖLÜM 6 -> Fragile Base Class problemi
// BÖLÜM 7 -> Hiyerarşiyi sığ tutmak: ne zaman kalıtım KULLANMA
//
// DART'A ÖZGÜ ÖNEMLİ NOT
// Dart'ta 'protected' yoktur. '_' ile başlayan üyeler dosyaya özeldir.
// Yani alt sınıfın _field'a erişebilmesi için base ile AYNI DOSYADA
// olması gerekir. Java/C#'tan gelenler bunu sürekli yanlış hatırlar.
// ============================================================================

// ############################################################################
//
//  BÖLÜM 1 — BASE CLASS (SUPERCLASS)
//
//  Bir base class şu soruya cevap verir: "Bu ailenin BÜTÜN üyelerinde
//  ortak olan ne var?"
//
//  Ortak olmayan bir şeyi buraya koyarsan, alt sınıfların bir kısmı onu
//  taşımak zorunda kalır ve mirası zorlamış olursun.
//
//  'abstract' -> bu sınıftan doğrudan nesne üretilemez. SchoolMember diye
//  bir kişi yoktur; öğrenci veya öğretmen vardır. Soyut olması yanlış
//  kullanımı derleme zamanında engelliyor.
//
// ############################################################################

abstract class SchoolMember {
  // ---- ORTAK ALANLAR ----
  final String id;
  final String fullName;
  final DateTime joinedOn;
  String _email;

  SchoolMember({
    required this.id,
    required this.fullName,
    required String email,
    DateTime? joinedOn,
  }) : _email = email.trim().toLowerCase(),
       joinedOn = joinedOn ?? DateTime.now() {
    // Doğrulama base'de: her alt sınıf için tekrar yazmaya gerek yok.
    if (id.trim().isEmpty) {
      throw ArgumentError.value(id, 'id', 'Kimlik boş olamaz');
    }
    if (fullName.trim().length < 2) {
      throw ArgumentError.value(fullName, 'fullName', 'İsim en az 2 karakter');
    }
    if (!_email.contains('@')) {
      throw ArgumentError.value(email, 'email', 'Geçersiz e-posta');
    }
  }

  // ---- ORTAK, HAZIR DAVRANIŞ (alt sınıflar bunu bedavaya alır) ----

  String get email => _email;

  int get yearsAtSchool => DateTime.now().difference(joinedOn).inDays ~/ 365;

  String get displayName => fullName;

  void updateEmail(String newEmail) {
    final normalized = newEmail.trim().toLowerCase();
    if (!normalized.contains('@')) {
      throw ArgumentError.value(newEmail, 'newEmail', 'Geçersiz e-posta');
    }
    _email = normalized;
  }

  // ---- ALT SINIFLARIN CEVAPLAMAK ZORUNDA OLDUĞU SORULAR ----
  //
  // Gövdesi yok. Bu, "bunu her alt sınıf kendi bilir" demek.
  // Yazmayı unutan alt sınıf DERLENMEZ.

  String get role;
  List<String> get permissions;

  // ---- TEMPLATE METHOD ----
  //
  // Kartın ŞEKLİ burada, İÇERİĞİ alt sınıflarda. Yarın kartın formatını
  // değiştirmek istersen tek bir yeri değiştirirsin; iki alt sınıf da
  // otomatik güncellenir. Kalıtımın asıl kazancı bu tür paylaşımdır.
  String idCard() {
    final buffer = StringBuffer();
    buffer.writeln('┌─────────────────────────────────────');
    buffer.writeln('│ ${role.toUpperCase()}');
    buffer.writeln('│ $displayName');
    buffer.writeln('│ No: $id');
    buffer.writeln('│ E-posta: $_email');
    buffer.writeln('│ Yetkiler: ${permissions.join(', ')}');
    buffer.write('└─────────────────────────────────────');
    return buffer.toString();
  }

  @override
  String toString() => '$role: $displayName ($id)';
}

// ############################################################################
//
//  BÖLÜM 2 — ALT SINIFLAR (SUBCLASS / DERIVED CLASS)
//
//  'extends' -> hem imzaları hem KODU devralır.
//  Alt sınıf sadece FARKLI olan kısmı yazar.
//
// ############################################################################

class Student extends SchoolMember {
  // ---- Sadece öğrencide olan alanlar ----
  final String classroom;
  final Map<String, int> _grades = {};

  Student({
    required super.id, // Dart 2.17+ kısayolu: super.id
    required super.fullName, // eskiden: required String id ... : super(id: id)
    required super.email,
    required this.classroom,
    super.joinedOn,
  }) {
    if (classroom.trim().isEmpty) {
      throw ArgumentError.value(classroom, 'classroom', 'Sınıf boş olamaz');
    }
  }

  // ---- Base'deki soruların cevapları ----
  @override
  String get role => 'Öğrenci';

  @override
  List<String> get permissions => ['ödevleri görüntüle', 'not görüntüle'];

  // ---- Base'deki davranışı GENİŞLETME (ezme değil) ----
  //
  // super.displayName ile base'in verdiği değeri alıp üstüne ekliyoruz.
  // Base'in mantığı değişirse buraya da yansır.
  @override
  String get displayName => '${super.displayName} ($classroom)';

  // ---- Sadece öğrenciye ait davranış ----
  Map<String, int> get grades => Map.unmodifiable(_grades);

  void recordGrade(String course, int score) {
    if (score < 0 || score > 100) {
      throw ArgumentError.value(score, 'score', '0-100 arası olmalı');
    }
    _grades[course] = score;
  }

  double get average {
    if (_grades.isEmpty) return 0;
    return _grades.values.reduce((a, b) => a + b) / _grades.length;
  }

  bool get isPassing => average >= 50;
}

class Teacher extends SchoolMember {
  final String branch;
  final List<String> _courses = [];

  static const int maxCourses = 5;

  Teacher({
    required super.id,
    required super.fullName,
    required super.email,
    required this.branch,
    super.joinedOn,
  }) {
    if (branch.trim().isEmpty) {
      throw ArgumentError.value(branch, 'branch', 'Branş boş olamaz');
    }
  }

  @override
  String get role => 'Öğretmen';

  @override
  List<String> get permissions => [
    'ödev oluştur',
    'not gir',
    'devamsızlık işle',
    if (isDepartmentHead) 'ders programı düzenle',
  ];

  @override
  String get displayName => '${super.displayName} — $branch';

  // ---- Sadece öğretmene ait ----
  List<String> get courses => List.unmodifiable(_courses);

  bool get isDepartmentHead => _courses.length >= 4;

  void assignCourse(String course) {
    if (_courses.length >= maxCourses) {
      throw StateError('En fazla $maxCourses ders atanabilir');
    }
    if (_courses.contains(course)) return;
    _courses.add(course);
  }

  int get weeklyHours => _courses.length * 4;
}

// ############################################################################
//
//  BÖLÜM 4 — CONSTRUCTOR SIRASI
//
//  Dart'ta sıra şudur:
//    1. Alt sınıfın initializer list'i (super'e giden argümanlar dahil)
//    2. Base sınıfın initializer list'i
//    3. Base sınıfın GÖVDESİ
//    4. Alt sınıfın GÖVDESİ
//
//  Sürprizi 3 ile 4 arasında: base'in gövdesi çalışırken alt sınıfın
//  gövdesi HENÜZ ÇALIŞMAMIŞTIR. Base constructor'ında override edilebilen
//  bir metot çağırırsan, alt sınıfın yarım kurulmuş halini görürsün.
//
// ############################################################################

class SiraBase {
  final String isim;

  SiraBase(this.isim) {
    print('    3. SiraBase gövdesi çalıştı');
    // TEHLİKE: burada tanit() çağırmak alt sınıfın henüz kurulmamış
    // alanlarına erişmek demektir. Bölüm 4 çıktısında sonucunu göreceksin.
    print('    -> base içinden tanit(): ${tanit()}');
  }

  String tanit() => 'ben base\'im';
}

class SiraAlt extends SiraBase {
  late final String ek;

  SiraAlt(String isim) : super(isim) {
    ek = 'hazır';
    print('    4. SiraAlt gövdesi çalıştı, ek="$ek"');
  }

  @override
  String tanit() {
    // 'ek' base constructor çalışırken henüz atanmamıştı.
    try {
      return 'ben altım, ek=$ek';
    } on Error {
      return 'HATA: "ek" henüz atanmamış (late field)';
    }
  }
}

// ############################################################################
//
//  BÖLÜM 6 — FRAGILE BASE CLASS (KIRILGAN TEMEL SINIF)
//
//  Kalıtımın en sinsi problemi. Alt sınıf, base'in İÇERİDE kendi
//  metotlarını nasıl çağırdığına bağımlı hale gelir. Base'in bu
//  iç detayı değişirse alt sınıf sessizce bozulur.
//
//  Aşağıdaki örnek Java'nın HashSet'inde yaşanmış gerçek bir hatanın
//  sadeleştirilmiş hali.
//
// ############################################################################

class Notepad {
  final List<String> _entries = [];

  void write(String entry) {
    _entries.add(entry);
  }

  /// Kritik detay: bu metot İÇERİDE write()'ı çağırıyor.
  /// Base'in dökümantasyonunda bu yazmıyor. Ama alt sınıf buna bağımlı.
  void writeAll(Iterable<String> entries) {
    for (final entry in entries) {
      write(entry);
    }
  }

  int get entryCount => _entries.length;
}

/// Amaç masum: kaç kez yazıldığını saymak.
/// İki metodu da override ediyoruz, ikisi de super'i çağırıyor.
/// Kod tamamen mantıklı görünüyor.
class CountingNotepad extends Notepad {
  int writeCount = 0;

  @override
  void write(String entry) {
    writeCount++;
    super.write(entry);
  }

  @override
  void writeAll(Iterable<String> entries) {
    writeCount += entries.length;
    super.writeAll(entries); // <-- bu, içeride bizim write()'ımızı çağırıyor
  }
}

/// ÇÖZÜM 1: Kalıtım yerine composition.
/// Notepad'i miras almıyoruz, İÇİMİZDE tutuyoruz. Notepad'in iç
/// detayları ne olursa olsun sayacımız doğru kalır.
class SafeCountingNotepad {
  final Notepad _notepad = Notepad();
  int writeCount = 0;

  void write(String entry) {
    writeCount++;
    _notepad.write(entry);
  }

  void writeAll(Iterable<String> entries) {
    for (final entry in entries) {
      write(entry); // kendi metodumuz, kontrol bizde
    }
  }

  int get entryCount => _notepad.entryCount;
}

// ############################################################################
//
//  BÖLÜM 7 — KALITIM KULLANILMAMASI GEREKEN DURUM
//
//  Okulda hem öğretmen hem veli olan biri var. Kalıtımla bunu
//  modelleyemezsin: Dart'ta (ve çoğu dilde) bir sınıf sadece TEK bir
//  sınıftan extends edebilir.
//
//  "TeacherWhoIsAlsoParent" diye bir sınıf yazmak çözüm değil; yarın
//  "hem öğrenci hem kulüp başkanı" çıkınca kombinasyon patlaması olur.
//
//  Doğru cevap: rolleri KALITIM değil, KOMPOZİSYON ile modellemek.
//
// ############################################################################

class ParentRole {
  final List<Student> children;

  const ParentRole(this.children);

  List<String> get permissions => [
    'çocuğun notlarını görüntüle',
    'veli toplantısına katıl',
  ];

  String get summary => children.map((c) => c.fullName).join(', ');
}

/// Öğretmen + veli. Kalıtımla değil, rolü İÇİNDE TUTARAK.
class StaffMember {
  final Teacher teacher;
  final ParentRole? parentRole;

  const StaffMember({required this.teacher, this.parentRole});

  bool get isAlsoParent => parentRole != null;

  List<String> get allPermissions => [
    ...teacher.permissions,
    if (parentRole != null) ...parentRole!.permissions,
  ];
}

// ============================================================================
// ÇALIŞTIRMA
// ============================================================================

void main() {
  // ==========================================================================
  print('=== BÖLÜM 1 + 2: BASE VE ALT SINIFLAR ===');

  final mehmet = Student(
    id: 'S-101',
    fullName: 'Mehmet Demir',
    email: 'Mehmet.Demir@Okul.COM',
    classroom: '9-A',
    joinedOn: DateTime(2024, 9, 1),
  );

  final ayse = Teacher(
    id: 'T-201',
    fullName: 'Ayşe Yılmaz',
    email: 'ayse@okul.com',
    branch: 'Matematik',
    joinedOn: DateTime(2019, 9, 1),
  );

  print(mehmet);
  print(ayse);
  print('');

  // ==========================================================================
  print('=== BÖLÜM 3: MİRAS ALINAN DAVRANIŞI KULLANMAK ===');

  // Bu üç üye Student ve Teacher'da HİÇ YAZILMADI. Base'den geldi.
  print('  updateEmail() — base\'den:');
  print('    önce : ${mehmet.email}   (constructor normalize etti)');
  mehmet.updateEmail('  YENI@Okul.com ');
  print('    sonra: ${mehmet.email}');

  print('  yearsAtSchool — base\'den:');
  print('    ${mehmet.fullName}: ${mehmet.yearsAtSchool} yıl');
  print('    ${ayse.fullName}: ${ayse.yearsAtSchool} yıl');

  print('  idCard() — base\'de tanımlı, içeriği alt sınıflardan:');
  ayse.assignCourse('Matematik 9');
  ayse.assignCourse('Matematik 10');
  print(mehmet.idCard());
  print(ayse.idCard());
  print('');

  print('  displayName — alt sınıf base\'i GENİŞLETİYOR:');
  print('    fullName      : ${mehmet.fullName}');
  print('    displayName   : ${mehmet.displayName}');
  print('    (Student, super.displayName\'i alıp sınıfı ekledi)');
  print('    öğretmende    : ${ayse.displayName}');
  print('');

  print('  Kendi özel davranışları:');
  mehmet.recordGrade('Matematik', 78);
  mehmet.recordGrade('Türkçe', 92);
  mehmet.recordGrade('Fizik', 45);
  print(
    '    ${mehmet.fullName} ortalama: ${mehmet.average.toStringAsFixed(1)}'
    ' — geçiyor mu: ${mehmet.isPassing}',
  );

  ayse.assignCourse('Geometri');
  ayse.assignCourse('Analiz');
  print(
    '    ${ayse.fullName} ders sayısı: ${ayse.courses.length},'
    ' haftalık ${ayse.weeklyHours} saat',
  );
  print('    Zümre başkanı mı: ${ayse.isDepartmentHead}');
  print(
    '    Yetkileri: ${ayse.permissions.length} adet (biri koşullu eklendi)',
  );
  print('');

  // ==========================================================================
  print('=== BÖLÜM 4: CONSTRUCTOR SIRASI ===');
  print('  1-2. adımlar initializer list (çıktı üretmiyor)');
  final _ = SiraAlt('deneme');
  print('');
  print('  Ders: base\'in gövdesi, alt sınıfın gövdesinden ÖNCE çalışır.');
  print('  Base constructor\'ında override edilebilen metot çağırma —');
  print('  alt sınıfın yarım kurulmuş halini görürsün.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 5: extends vs implements ===');

  print('  extends    -> imzaları VE kodu devralır.');
  print('                Student, updateEmail()\'i yazmadı ama sahip.');
  print('  implements -> sadece imzaları devralır, kodu YAZMAK ZORUNDA.');
  print('                Gün 8\'deki PaymentMethod böyleydi.');
  print('');
  print('  Seçim kuralı:');
  print('    Kod paylaşmak istiyorsan       -> extends');
  print(
    '    Sadece sözleşme kurmak istiyorsan -> implements / abstract interface',
  );
  print('  Şüphedeysen implements seç: bağı daha gevşektir.');
  print('');

  // Polimorfizmin ön tadı: ikisi de SchoolMember olarak işlenebiliyor.
  final herkes = <SchoolMember>[mehmet, ayse];
  print('  Ortak tip üzerinden gezinme:');
  for (final kisi in herkes) {
    print('    ${kisi.role.padRight(10)} ${kisi.displayName}');
  }
  print('  (Bu polymorphism. Yarınki günün konusu.)');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 6: FRAGILE BASE CLASS ===');

  final sayan = CountingNotepad();
  sayan.writeAll(['satır 1', 'satır 2', 'satır 3']);

  print('  CountingNotepad.writeAll ile 3 satır yazıldı:');
  print('    gerçek kayıt sayısı : ${sayan.entryCount}');
  print('    writeCount          : ${sayan.writeCount}   <-- İKİ KATI');
  print('');
  print('  Sebep: base\'in writeAll\'ı içeride write() çağırıyor.');
  print('  Biz her ikisini de override ettik, sayaç iki kez arttı.');
  print('  Kodun ikisinde de hata yok; hata BİRLİKTE çalışmalarında.');
  print('');
  print('  Daha kötüsü: base yarın writeAll\'ı write çağırmayacak şekilde');
  print('  değiştirirse, sayaç bu sefer eksik sayar. Alt sınıf, base\'in');
  print('  YAZILI OLMAYAN bir iç detayına bağımlı — kırılganlık bu.');
  print('');

  final guvenli = SafeCountingNotepad();
  guvenli.writeAll(['satır 1', 'satır 2', 'satır 3']);
  print('  Composition ile aynı iş:');
  print('    gerçek kayıt sayısı : ${guvenli.entryCount}');
  print('    writeCount          : ${guvenli.writeCount}   <-- doğru');
  print('  Notepad\'i miras almadık, içimizde tuttuk. İç detayları');
  print('  ne olursa olsun bizi etkilemiyor.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 7: HİYERARŞİYİ SIĞ TUTMAK ===');

  final ayseninCocugu = Student(
    id: 'S-102',
    fullName: 'Elif Yılmaz',
    email: 'elif@okul.com',
    classroom: '6-B',
  );

  final ayseKadro = StaffMember(
    teacher: ayse,
    parentRole: ParentRole([ayseninCocugu]),
  );

  print('  ${ayse.fullName} hem öğretmen hem veli.');
  print('  Kalıtımla imkânsız: Dart\'ta tek bir sınıftan extends edilir.');
  print('  Çözüm: rolü İÇİNDE TUTMAK (composition).');
  print('    Veli mi: ${ayseKadro.isAlsoParent}');
  print('    Çocuğu : ${ayseKadro.parentRole!.summary}');
  print('    Toplam yetki: ${ayseKadro.allPermissions.length} adet');
  for (final y in ayseKadro.allPermissions) {
    print('      - $y');
  }
  print('');

  print('  --- KALITIM KULLANMADAN ÖNCE ÜÇ TEST ---');
  print('  1. IS-A testi: "Öğrenci BİR okul üyesidir" -> doğru cümle mi?');
  print('     "Araba BİR motordur" -> hayır, arabanın motoru VARDIR.');
  print('     Cümle tuhaf geliyorsa kalıtım değil, kompozisyon lazım.');
  print('');
  print('  2. Yerine geçme testi (Liskov):');
  print('     Alt sınıfı base\'in gittiği her yere koyabilir misin?');
  print('     Alt sınıf base\'in bir metodunu "desteklemiyorum" diye');
  print('     hata fırlatıyorsa, o kalıtım yanlıştır.');
  print('');
  print('  3. Derinlik testi: 2 seviyeden fazlaysa dur.');
  print('     Her seviye, üstündeki her şeye bağımlıdır. 5 seviyelik bir');
  print('     hiyerarşide en alttaki sınıfı anlamak için 5 dosya okursun.');
  print('');
  print('  Pratik varsayılan: KOMPOZİSYONLA BAŞLA. Kalıtımı sadece');
  print('  gerçekten "aynı ailenin üyeleri" olan ve ortak kodu paylaşan');
  print('  tipler için kullan — bugünkü SchoolMember gibi.');
}
