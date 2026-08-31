// ============================================================================
// GÜN 20 — CAPSTONE: DERS KAYIT SİSTEMİ  (1/2: MODEL)
//
// Çalıştırmak için:  dart run gun20_main.dart
// (gun20_kayit_sistemi.dart ile aynı klasörde olmalı)
//
// Bu dosya 20 günün özeti. Her bölümün başında hangi günün fikri
// olduğu yazıyor.
//
// KAPSAM SINIRI (bilinçli):
//   - Veritabanı yok, ağ yok, arayüz yok. Sadece domain modeli.
//   - Desen sayısı üç: Strategy (politikalar), Composite (politika
//     birleştirme), Factory (Registrar üzerinden kayıt üretimi).
//     Başka desen EKLENMEDİ çünkü bir sorunu çözmüyorlardı.
// ============================================================================

import 'dart:collection';

// ############################################################################
//  DEĞER NESNELERİ  (Gün 9)
//
//  Kimlikleri yok, değerleriyle tanımlanırlar, değişmezler.
//  Düz String yerine bunları kullanmak yanlış değeri yanlış yere
//  vermeyi DERLEME ZAMANINDA engelliyor (Gün 10).
// ############################################################################

class StudentId {
  final String value;
  const StudentId._(this.value);

  factory StudentId.parse(String raw) {
    final cleaned = raw.trim().toUpperCase();
    if (!RegExp(r'^S-\d{4}-\d{4}$').hasMatch(cleaned)) {
      throw FormatException('Öğrenci no S-2026-1234 biçiminde olmalı: $raw');
    }
    return StudentId._(cleaned);
  }

  @override
  bool operator ==(Object other) =>
      identical(this, other) || (other is StudentId && other.value == value);
  @override
  int get hashCode => value.hashCode;
  @override
  String toString() => value;
}

class CourseCode {
  final String value;
  const CourseCode._(this.value);

  factory CourseCode.parse(String raw) {
    final cleaned = raw.trim().toUpperCase().replaceAll(' ', '');
    if (!RegExp(r'^[A-Z]{2,4}\d{3}$').hasMatch(cleaned)) {
      throw FormatException('Ders kodu MAT101 biçiminde olmalı: $raw');
    }
    return CourseCode._(cleaned);
  }

  @override
  bool operator ==(Object other) =>
      identical(this, other) || (other is CourseCode && other.value == value);
  @override
  int get hashCode => value.hashCode;
  @override
  String toString() => value;
}

enum Semester {
  fall('Güz'),
  spring('Bahar'),
  summer('Yaz');

  const Semester(this.label);
  final String label;
}

class Term {
  final int year;
  final Semester semester;

  const Term(this.year, this.semester);

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is Term && other.year == year && other.semester == semester);
  @override
  int get hashCode => Object.hash(year, semester);
  @override
  String toString() => '$year ${semester.label}';
}

/// Not: 0-100 arası. Harf ve katsayı HESAPLANAN değerler — saklanmıyor,
/// dolayısıyla asla tutarsız olamazlar (Gün 2, 6).
class Grade {
  final int score;
  const Grade._(this.score);

  factory Grade.of(int score) {
    if (score < 0 || score > 100) {
      throw ArgumentError.value(score, 'score', '0-100 arası olmalı');
    }
    return Grade._(score);
  }

  String get letter => switch (score) {
    >= 90 => 'AA',
    >= 85 => 'BA',
    >= 75 => 'BB',
    >= 70 => 'CB',
    >= 60 => 'CC',
    >= 55 => 'DC',
    >= 50 => 'DD',
    _ => 'FF',
  };

  double get points => switch (score) {
    >= 90 => 4.0,
    >= 85 => 3.5,
    >= 75 => 3.0,
    >= 70 => 2.5,
    >= 60 => 2.0,
    >= 55 => 1.5,
    >= 50 => 1.0,
    _ => 0.0,
  };

  bool get isPassing => score >= 50;

  @override
  bool operator ==(Object other) =>
      identical(this, other) || (other is Grade && other.score == score);
  @override
  int get hashCode => score.hashCode;
  @override
  String toString() => '$letter ($score)';
}

/// Gün 10: iki bool yerine enum. "Hem tamamlanmış hem çekilmiş" gibi
/// anlamsız bir durum İFADE EDİLEMİYOR.
enum EnrollmentStatus { active, completed, dropped }

// ############################################################################
//  KAYIT  (Enrollment)  — Gün 4 (iş birliği), 6, 7
//
//  Öğrenci ile dersi birbirine bağlayan varlık.
//
//  Constructor private: kayıt üretmenin TEK yolu Registrar. Böylece
//  "öğrencinin listesine eklenmemiş kayıt" veya "kontenjan düşürülmeden
//  oluşmuş kayıt" gibi tutarsız durumlar oluşamıyor.
// ############################################################################

class Enrollment {
  final Student student;
  final Course course;
  final Term term;

  EnrollmentStatus _status = EnrollmentStatus.active;
  Grade? _grade;

  Enrollment._({
    required this.student,
    required this.course,
    required this.term,
  });

  EnrollmentStatus get status => _status;
  Grade? get grade => _grade;
  bool get isActive => _status == EnrollmentStatus.active;
  bool get isCompleted => _status == EnrollmentStatus.completed;
  bool get isPassed => _grade?.isPassing ?? false;

  /// Dersi notla kapat.
  void complete(Grade grade) {
    if (_status != EnrollmentStatus.active) {
      throw StateError(
        'Sadece aktif kayıt notlandırılabilir (durum: ${_status.name})',
      );
    }
    _grade = grade;
    _status = EnrollmentStatus.completed;
    course._releaseSeat();
    student._recomputeActiveCredits();
    _checkInvariants();
  }

  /// Dersten çekil.
  void drop() {
    if (_status != EnrollmentStatus.active) {
      throw StateError(
        'Sadece aktif kayıttan çekilinebilir (durum: ${_status.name})',
      );
    }
    _status = EnrollmentStatus.dropped;
    course._releaseSeat();
    student._recomputeActiveCredits();
    _checkInvariants();
  }

  /// INVARIANT'LAR (Gün 7)
  ///   E1: tamamlanmış kaydın notu olmalı
  ///   E2: tamamlanmamış kaydın notu OLMAMALI
  void _checkInvariants() {
    if (_status == EnrollmentStatus.completed && _grade == null) {
      throw StateError('İHLAL E1: tamamlanmış kaydın notu yok');
    }
    if (_status != EnrollmentStatus.completed && _grade != null) {
      throw StateError('İHLAL E2: tamamlanmamış kaydın notu var');
    }
  }

  @override
  String toString() {
    final not = _grade == null ? '' : ' — $_grade';
    return '${course.code} ${course.title} [${_status.name}]$not';
  }
}

// ############################################################################
//  DERS  (Course)  — Gün 6, 7, 10
// ############################################################################

class Course {
  final CourseCode code;
  final String title;
  final int credits;
  final int capacity;
  final Set<CourseCode> prerequisites;

  int _seatsTaken = 0;
  final List<Enrollment> _roster = [];

  Course._({
    required this.code,
    required this.title,
    required this.credits,
    required this.capacity,
    required this.prerequisites,
  });

  /// DOMAIN DİLİ (Gün 10): üniversitede buna "ders açmak" denir.
  factory Course.open({
    required String code,
    required String title,
    required int credits,
    required int capacity,
    List<String> prerequisites = const [],
  }) {
    if (title.trim().length < 2) {
      throw ArgumentError.value(title, 'title', 'Ders adı en az 2 karakter');
    }
    if (credits < 1 || credits > 10) {
      throw ArgumentError.value(credits, 'credits', '1-10 arası olmalı');
    }
    if (capacity < 1 || capacity > 500) {
      throw ArgumentError.value(capacity, 'capacity', '1-500 arası olmalı');
    }

    return Course._(
      code: CourseCode.parse(code),
      title: title.trim(),
      credits: credits,
      capacity: capacity,
      prerequisites: prerequisites.map(CourseCode.parse).toSet(),
    );
  }

  // ---- Salt okunur erişim (Gün 6) ----
  int get seatsTaken => _seatsTaken;
  int get seatsLeft => capacity - _seatsTaken;
  bool get isFull => seatsLeft <= 0;
  bool get hasPrerequisites => prerequisites.isNotEmpty;

  UnmodifiableListView<Enrollment> get roster => UnmodifiableListView(_roster);

  List<Enrollment> get activeEnrollments =>
      _roster.where((e) => e.isActive).toList();

  double? get averageScore {
    final notlar = _roster
        .where((e) => e.isCompleted)
        .map((e) => e.grade!.score)
        .toList();
    if (notlar.isEmpty) return null;
    return notlar.reduce((a, b) => a + b) / notlar.length;
  }

  // ---- Sadece bu kütüphanenin çağırabileceği işlemler ----
  void _takeSeat(Enrollment enrollment) {
    if (isFull) {
      throw StateError('$code kontenjanı dolu');
    }
    _seatsTaken++;
    _roster.add(enrollment);
    _checkInvariants();
  }

  void _releaseSeat() {
    _seatsTaken--;
    _checkInvariants();
  }

  /// INVARIANT'LAR
  ///   C1: dolu koltuk sayısı 0 ile kontenjan arasında
  ///   C2: dolu koltuk sayısı = aktif kayıt sayısı
  void _checkInvariants() {
    if (_seatsTaken < 0 || _seatsTaken > capacity) {
      throw StateError('İHLAL C1: $code koltuk sayısı $_seatsTaken');
    }
    final aktif = _roster.where((e) => e.isActive).length;
    if (_seatsTaken != aktif) {
      throw StateError(
        'İHLAL C2: $code koltuk $_seatsTaken, aktif kayıt $aktif',
      );
    }
  }

  @override
  bool operator ==(Object other) =>
      identical(this, other) || (other is Course && other.code == code);
  @override
  int get hashCode => code.hashCode;
  @override
  String toString() =>
      '$code $title ($credits kredi, $seatsLeft/$capacity boş)';
}

// ############################################################################
//  ÖĞRENCİ  (Student)  — Gün 6, 7, 9
//
//  ENTITY: kimliği var, eşitlik id'ye göre. İsmi değişse de aynı öğrenci.
// ############################################################################

class Student {
  final StudentId id;
  final Term startedIn;

  String _fullName;
  final List<Enrollment> _enrollments = [];
  int _activeCredits = 0;

  Student._({
    required this.id,
    required String fullName,
    required this.startedIn,
  }) : _fullName = fullName;

  factory Student.register({
    required String studentId,
    required String fullName,
    required Term startedIn,
  }) {
    if (fullName.trim().length < 2) {
      throw ArgumentError.value(fullName, 'fullName', 'İsim en az 2 karakter');
    }
    return Student._(
      id: StudentId.parse(studentId),
      fullName: fullName.trim(),
      startedIn: startedIn,
    );
  }

  // ---- Salt okunur erişim ----
  String get fullName => _fullName;
  int get activeCredits => _activeCredits;

  UnmodifiableListView<Enrollment> get enrollments =>
      UnmodifiableListView(_enrollments);

  List<Enrollment> get activeEnrollments =>
      _enrollments.where((e) => e.isActive).toList();

  Transcript get transcript => Transcript(_enrollments);

  bool hasPassed(CourseCode code) => _enrollments.any(
    (e) => e.course.code == code && e.isCompleted && e.isPassed,
  );

  bool isEnrolledIn(CourseCode code, Term term) => _enrollments.any(
    (e) => e.course.code == code && e.term == term && e.isActive,
  );

  // ---- Kontrollü güncelleme ----
  void rename(String newName) {
    if (newName.trim().length < 2) {
      throw ArgumentError.value(newName, 'newName', 'İsim en az 2 karakter');
    }
    _fullName = newName.trim();
  }

  void _attach(Enrollment enrollment) {
    _enrollments.add(enrollment);
    _recomputeActiveCredits();
  }

  void _recomputeActiveCredits() {
    _activeCredits = _enrollments
        .where((e) => e.isActive)
        .fold(0, (sum, e) => sum + e.course.credits);
    _checkInvariants();
  }

  /// INVARIANT'LAR
  ///   S1: aktif kredi toplamı = aktif kayıtların kredi toplamı
  ///   S2: aynı ders + aynı dönem için birden fazla AKTİF kayıt olamaz
  void _checkInvariants() {
    final beklenen = _enrollments
        .where((e) => e.isActive)
        .fold<int>(0, (sum, e) => sum + e.course.credits);
    if (_activeCredits != beklenen) {
      throw StateError('İHLAL S1: kredi $_activeCredits, beklenen $beklenen');
    }

    final anahtarlar = _enrollments
        .where((e) => e.isActive)
        .map((e) => '${e.course.code}@${e.term}')
        .toList();
    if (anahtarlar.toSet().length != anahtarlar.length) {
      throw StateError('İHLAL S2: aynı derse mükerrer aktif kayıt');
    }
  }

  @override
  bool operator ==(Object other) =>
      identical(this, other) || (other is Student && other.id == id);
  @override
  int get hashCode => id.hashCode;
  @override
  String toString() => '$_fullName ($id)';
}

// ############################################################################
//  TRANSKRİPT  — Gün 16 (SRP)
//
//  Ortalama hesabı Student'ın işi değil. Öğrenci kim olduğunu bilir;
//  transkript hesabı AYRI bir sorumluluk ve AYRI bir değişme sebebi
//  (yönetmelik değişirse sadece burası değişir).
// ############################################################################

class Transcript {
  final List<Enrollment> _completed;

  Transcript(List<Enrollment> enrollments)
    : _completed = enrollments.where((e) => e.isCompleted).toList();

  int get completedCourses => _completed.length;

  int get attemptedCredits =>
      _completed.fold(0, (sum, e) => sum + e.course.credits);

  int get earnedCredits => _completed
      .where((e) => e.isPassed)
      .fold(0, (sum, e) => sum + e.course.credits);

  double get gpa {
    if (_completed.isEmpty) return 0;
    final agirlikli = _completed.fold<double>(
      0,
      (sum, e) => sum + e.grade!.points * e.course.credits,
    );
    return agirlikli / attemptedCredits;
  }

  /// Hiç ders tamamlamamış öğrenci "kötü durumda" sayılmaz.
  bool get isInGoodStanding => _completed.isEmpty || gpa >= 2.0;

  String summary() => _completed.isEmpty
      ? 'Henüz tamamlanmış ders yok'
      : 'GNO ${gpa.toStringAsFixed(2)} · '
            '$earnedCredits/$attemptedCredits kredi · $completedCourses ders';
}

// ############################################################################
//  KAYIT POLİTİKASI  — Gün 8, 13 (sözleşme) + Gün 16 (OCP)
//
//  Kayıt kuralları burada. Yeni kural eklemek = yeni sınıf.
//  Registrar'a, Student'a, Course'a DOKUNULMUYOR.
//
//  Sözleşmede "kontenjan", "önkoşul", "kredi" gibi hiçbir kelime yok —
//  sadece "bu kayıt yapılabilir mi, yapılamazsa neden".
// ############################################################################

class PolicyResult {
  final bool allowed;
  final String? reason;

  const PolicyResult.allow() : allowed = true, reason = null;

  const PolicyResult.deny(String this.reason) : allowed = false;
}

abstract interface class EnrollmentPolicy {
  String get name;
  PolicyResult check(Student student, Course course, Term term);
}

class CapacityPolicy implements EnrollmentPolicy {
  const CapacityPolicy();

  @override
  String get name => 'Kontenjan';

  @override
  PolicyResult check(Student s, Course c, Term t) => c.isFull
      ? PolicyResult.deny('${c.code} kontenjanı dolu')
      : const PolicyResult.allow();
}

class PrerequisitePolicy implements EnrollmentPolicy {
  const PrerequisitePolicy();

  @override
  String get name => 'Önkoşul';

  @override
  PolicyResult check(Student s, Course c, Term t) {
    final eksik = c.prerequisites.where((p) => !s.hasPassed(p)).toList();
    if (eksik.isEmpty) return const PolicyResult.allow();
    return PolicyResult.deny(
      '${c.code} için önkoşul eksik: ${eksik.join(", ")}',
    );
  }
}

class CreditLimitPolicy implements EnrollmentPolicy {
  final int maxCredits;
  const CreditLimitPolicy({this.maxCredits = 24});

  @override
  String get name => 'Kredi sınırı ($maxCredits)';

  @override
  PolicyResult check(Student s, Course c, Term t) {
    final yeni = s.activeCredits + c.credits;
    if (yeni > maxCredits) {
      return PolicyResult.deny(
        'Kredi sınırı aşılır: '
        '${s.activeCredits} + ${c.credits} > $maxCredits',
      );
    }
    return const PolicyResult.allow();
  }
}

class NoDuplicatePolicy implements EnrollmentPolicy {
  const NoDuplicatePolicy();

  @override
  String get name => 'Mükerrer kayıt';

  @override
  PolicyResult check(Student s, Course c, Term t) => s.isEnrolledIn(c.code, t)
      ? PolicyResult.deny('${c.code} dersine bu dönem zaten kayıtlı')
      : const PolicyResult.allow();
}

class AlreadyPassedPolicy implements EnrollmentPolicy {
  const AlreadyPassedPolicy();

  @override
  String get name => 'Geçilmiş ders';

  @override
  PolicyResult check(Student s, Course c, Term t) => s.hasPassed(c.code)
      ? PolicyResult.deny('${c.code} zaten başarıyla tamamlanmış')
      : const PolicyResult.allow();
}

/// COMPOSITE (Gün 14): birden çok politikayı tek politika gibi sunar.
/// Hepsi geçerse izin verilir; ilk reddeden sebebi döndürür.
class AllOfPolicy implements EnrollmentPolicy {
  final List<EnrollmentPolicy> policies;
  const AllOfPolicy(this.policies);

  @override
  String get name => policies.map((p) => p.name).join(' + ');

  @override
  PolicyResult check(Student s, Course c, Term t) {
    for (final policy in policies) {
      final sonuc = policy.check(s, c, t);
      if (!sonuc.allowed) return sonuc;
    }
    return const PolicyResult.allow();
  }
}

/// Standart yönetmelik.
///
/// SIRA ÖNEMLİ: ilk reddeden kural kazanıyor, o yüzden sıra "hangi sebebi
/// duyacak" sorusunun cevabı. Önce ÖĞRENCİYE ait engeller (mükerrer, geçilmiş,
/// önkoşul, kredi), sonra DERSE ait engel (kontenjan) geliyor. Kredi sınırını
/// aşan öğrenciye "ders dolu" demek yanıltıcı olurdu; ders boşalsa bile
/// kaydolamayacak.
EnrollmentPolicy standardPolicy({int maxCredits = 24}) => AllOfPolicy([
  const NoDuplicatePolicy(),
  const AlreadyPassedPolicy(),
  const PrerequisitePolicy(),
  CreditLimitPolicy(maxCredits: maxCredits),
  const CapacityPolicy(),
]);

// ############################################################################
//  KAYIT İŞLEMİ  (Registrar)  — Gün 4, 13, 16
//
//  SORUMLULUK: kayıt işlemini yürütmek. Kuralları BİLMİYOR (politikaya
//  soruyor), not hesaplamıyor (transkriptin işi), veri saklamıyor.
//
//  Politikayı dışarıdan alıyor — dependency inversion (Gün 13).
// ############################################################################

class EnrollmentDenied implements Exception {
  final String reason;
  const EnrollmentDenied(this.reason);
  @override
  String toString() => 'Kayıt reddedildi: $reason';
}

class Registrar {
  final EnrollmentPolicy policy;
  final Term term;

  const Registrar({required this.policy, required this.term});

  /// QUERY (Gün 4): durumu değiştirmez.
  PolicyResult canEnroll(Student student, Course course) =>
      policy.check(student, course, term);

  /// COMMAND (Gün 4) + FACTORY (Gün 3).
  ///
  /// Enrollment'ın constructor'ı private. Kayıt üretmenin tek yolu bu
  /// metot ve burada üç şey BİRLİKTE oluyor: nesne üretimi, koltuk
  /// düşümü, öğrenciye bağlama. Yarım kalmış durum oluşamıyor (Gün 5).
  Enrollment enroll(Student student, Course course) {
    // FAIL FAST (Gün 7): önce bütün kontroller, sonra değişiklik.
    final karar = policy.check(student, course, term);
    if (!karar.allowed) {
      throw EnrollmentDenied(karar.reason!);
    }

    final enrollment = Enrollment._(
      student: student,
      course: course,
      term: term,
    );

    course._takeSeat(enrollment);
    student._attach(enrollment);

    return enrollment;
  }

  /// Toplu kayıt; reddedilenleri gerekçesiyle raporlar.
  ({List<Enrollment> enrolled, List<String> rejected}) enrollAll(
    Student student,
    List<Course> courses,
  ) {
    final basarili = <Enrollment>[];
    final redler = <String>[];

    for (final course in courses) {
      try {
        basarili.add(enroll(student, course));
      } on EnrollmentDenied catch (e) {
        redler.add(e.reason);
      }
    }
    return (enrolled: basarili, rejected: redler);
  }
}
