// ============================================================================
// GÜN 1 — OOP TEMELLERİ  (Dart)
//
// Çalıştırmak için:  dart run gun1_oop_temelleri.dart
// veya dartpad.dev'e yapıştır.
//
// Bu dosya 3 bölümden oluşuyor:
//   BÖLÜM 1 -> Aynı özelliğin PROSEDÜREL hâli
//   BÖLÜM 2 -> Aynı özelliğin OOP (class) hâli
//   BÖLÜM 3 -> Bir domain'deki nesneleri belirleme alıştırması
// ============================================================================

// ============================================================================
// BÖLÜM 1 — PROSEDÜREL YAKLAŞIM
//
// Fikir: VERİ ayrı durur (burada bir Map), DAVRANIŞ ayrı durur (top-level
// fonksiyonlar). Fonksiyonlar veriyi dışarıdan parametre olarak alır.
// ============================================================================

/// Yeni bir görev "verisi" üretir. Sadece veri; hiçbir davranışı yok.
Map<String, dynamic> createTaskData(String title, String owner) {
  return {'title': title, 'owner': owner, 'isDone': false, 'completedAt': null};
}

/// Görevi tamamlanmış olarak işaretler.
/// Dikkat: verinin nasıl kurulduğunu bu fonksiyonun EZBERLEMESİ gerekiyor.
/// 'isDone' anahtarını yanlış yazarsan Dart uyarmaz, sessizce null döner.
Map<String, dynamic> markTaskAsDone(Map<String, dynamic> task) {
  if (task['isDone'] == true) {
    print('${task['title']} zaten tamamlanmış.');
    return task;
  }
  task['isDone'] = true;
  task['completedAt'] = DateTime.now();
  print('${task['title']} tamamlandı.');
  return task;
}

/// Görevi okunabilir bir metne çevirir.
String describeTaskData(Map<String, dynamic> task) {
  final status = (task['isDone'] == true) ? 'Tamamlandı' : 'Bekliyor';
  return '${task['title']} (${task['owner']}) -> $status';
}

// ============================================================================
// BÖLÜM 2 — OOP YAKLAŞIMI
//
// Fikir: Aynı veriye ait DAVRANIŞ, verinin yanına taşınıyor.
// Görevin ne olduğunu ve ne yapabildiğini tek bir yerde tanımlıyoruz.
// ============================================================================

/// SINIF (class): bir görevin neye benzediğinin ve ne yapabildiğinin şablonu.
class Task {
  // ---- ÖZELLİKLER (attribute / field) = nesnenin verisi ----

  // 'final' -> bir kez atanır, sonra değişmez.
  final String title;
  final String owner;

  // Başında '_' olan alanlar dosya dışına KAPALIDIR (encapsulation).
  // Dışarıdan kimse task._isDone = true diyemez; sadece markAsDone() ile değişir.
  bool _isDone = false;
  DateTime? _completedAt; // '?' -> null olabilir (henüz tamamlanmadıysa)

  // ---- CONSTRUCTOR: nesne doğarken çalışan özel fonksiyon ----
  // 'required' -> bu parametreler zorunlu. Yarım bir Task üretemezsin.
  Task({required this.title, required this.owner});

  // ---- GETTER: dışarıya okuma izni ver, yazma izni verme ----
  bool get isDone => _isDone;
  DateTime? get completedAt => _completedAt;

  // ---- METOTLAR (method) = nesnenin davranışı ----

  /// Prosedürel versiyonla aynı iş — ama parametre almıyor ve değer döndürmüyor.
  /// Çünkü üzerinde çalıştığı veri zaten kendi içinde: 'this'.
  void markAsDone() {
    if (_isDone) {
      print('$title zaten tamamlanmış.');
      return;
    }
    _isDone = true;
    _completedAt = DateTime.now();
    print('$title tamamlandı.');
  }

  String describe() {
    final status = _isDone ? 'Tamamlandı' : 'Bekliyor';
    return '$title ($owner) -> $status';
  }
}

// ============================================================================
// BÖLÜM 3 — DOMAIN'DEKİ NESNELERİ BELİRLEME
//
// Domain: bir okul ödev takip sistemi.
// Aşağıdaki her sınıfın TEK bir sorumluluğu var. Bir sınıfı anlatırken
// "ve" demek zorunda kalıyorsan, muhtemelen ikiye bölünmesi gerekiyor.
// ============================================================================

/// Sorumluluk: bir öğrenciyi temsil etmek ve teslimlerini saklamak.
class Student {
  final String id;
  final String fullName;
  final List<Submission> _submissions = [];

  Student({required this.id, required this.fullName});

  /// Liste dışarıya kopya olarak veriliyor ki dışarıdan eleman eklenemesin.
  List<Submission> get submissions => List.unmodifiable(_submissions);

  int get submissionCount => _submissions.length;

  void addSubmission(Submission submission) {
    _submissions.add(submission);
  }
}

/// Sorumluluk: bir öğretmeni temsil etmek ve ödev oluşturmak.
class Teacher {
  final String id;
  final String fullName;
  final String branch;

  Teacher({required this.id, required this.fullName, required this.branch});

  /// Öğretmen "ödev verir" — bu davranış doğal olarak Teacher'a ait.
  Assignment createAssignment(String title, DateTime dueDate) {
    return Assignment(title: title, dueDate: dueDate, createdBy: this);
  }
}

/// Sorumluluk: bir ödevin ne olduğunu ve ne zaman teslim edileceğini bilmek.
class Assignment {
  final String title;
  final DateTime dueDate;
  final Teacher createdBy;

  Assignment({
    required this.title,
    required this.dueDate,
    required this.createdBy,
  });

  /// "Süresi geçti mi?" sorusunun cevabı, ödevin kendi bilgisiyle hesaplanır.
  /// Bu yüzden bu mantık başka bir yerde değil, burada durmalı.
  bool get isOverdue => DateTime.now().isAfter(dueDate);

  int get daysLeft => dueDate.difference(DateTime.now()).inDays;
}

/// Sorumluluk: "hangi öğrenci hangi ödevi ne zaman teslim etti ve kaç aldı"
/// bilgisini tutmak. Student ile Assignment'ı birbirine bağlayan nesne.
class Submission {
  final Student student;
  final Assignment assignment;
  final DateTime submittedAt;
  int? _score; // henüz notlandırılmadıysa null

  Submission({
    required this.student,
    required this.assignment,
    required this.submittedAt,
  });

  int? get score => _score;
  bool get isGraded => _score != null;

  /// Geç teslim edilip edilmediği, kayıt anında hesaplanabilir bir bilgi.
  bool get isLate => submittedAt.isAfter(assignment.dueDate);

  /// Kural kontrolü nesnenin İÇİNDE. Böylece 150 puan verilmesi imkânsız.
  void assignScore(int value) {
    if (value < 0 || value > 100) {
      print('Geçersiz not: $value (0-100 arası olmalı)');
      return;
    }
    _score = value;
  }

  String describe() {
    final scoreText = isGraded ? '$_score puan' : 'notlandırılmadı';
    final lateText = isLate ? ' [GEÇ]' : '';
    return '${student.fullName} -> ${assignment.title}: $scoreText$lateText';
  }
}

// ============================================================================
// ÇALIŞTIRMA
// ============================================================================

void main() {
  print('=== BÖLÜM 1: PROSEDÜREL ===');
  var taskData = createTaskData('OOP dersini bitir', 'ben');
  print(describeTaskData(taskData));
  taskData = markTaskAsDone(taskData);
  print(describeTaskData(taskData));
  markTaskAsDone(taskData); // ikinci kez -> "zaten tamamlanmış"

  // Prosedürel yaklaşımın açığı: hiçbir kural bunu engellemiyor.
  taskData['isDone'] = false; // görev sihirli şekilde geri açıldı
  taskData['owner'] = 12345; // string olması gerekiyordu, Dart susuyor
  print('Bozulmuş veri: ${describeTaskData(taskData)}');

  print('');
  print('=== BÖLÜM 2: OOP ===');
  final task = Task(title: 'OOP dersini bitir', owner: 'ben');
  print(task.describe());
  task.markAsDone();
  print(task.describe());
  task.markAsDone(); // ikinci kez -> "zaten tamamlanmış"

  // task._isDone = false;  <-- BU SATIRI AÇMAYI DENE, derlenmez.
  // task.isDone = false;   <-- BU DA DERLENMEZ (sadece getter var).
  print('Dışarıdan bozulamıyor: ${task.describe()}');

  print('');
  print('=== BÖLÜM 3: DOMAIN NESNELERİ ===');

  final teacher = Teacher(
    id: 'T1',
    fullName: 'Ayşe Yılmaz',
    branch: 'Matematik',
  );
  final student = Student(id: 'S1', fullName: 'Mehmet Demir');

  final assignment = teacher.createAssignment(
    'Türev Alıştırmaları',
    DateTime.now().add(const Duration(days: 3)),
  );

  print('Ödev: ${assignment.title}');
  print(
    'Veren: ${assignment.createdBy.fullName} (${assignment.createdBy.branch})',
  );
  print(
    'Kalan gün: ${assignment.daysLeft}, süresi geçti mi: ${assignment.isOverdue}',
  );

  final submission = Submission(
    student: student,
    assignment: assignment,
    submittedAt: DateTime.now(),
  );
  student.addSubmission(submission);

  print(submission.describe());
  submission.assignScore(150); // geçersiz -> reddedilir
  submission.assignScore(85); // geçerli
  print(submission.describe());
  print('${student.fullName} toplam teslim: ${student.submissionCount}');
}
