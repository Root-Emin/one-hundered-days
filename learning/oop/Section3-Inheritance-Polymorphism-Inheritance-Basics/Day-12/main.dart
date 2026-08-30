// ============================================================================
// GÜN 12 — METHOD OVERRIDING VE POLYMORPHISM  (Dart)
//
// Çalıştırmak için:  dart run gun12_overriding_ve_polymorphism.dart
// veya dartpad.dev'e yapıştır.
//
// BÖLÜM 1 -> Base sınıf ve override edilebilir metotlar
// BÖLÜM 2 -> Dört override stratejisi
// BÖLÜM 3 -> Substitution: karışık listeyi tek tip üzerinden işlemek
// BÖLÜM 4 -> Dynamic dispatch nasıl çalışır
// BÖLÜM 5 -> Override edilMEYEN şeyler (static, alan)
// BÖLÜM 6 -> Kötü override: sözleşmeyi bozmak
// ============================================================================

int _clamp(int value, int low, int high) =>
    value < low ? low : (value > high ? high : value);


// ============================================================================
// YARDIMCI VERİ NESNELERİ
// ============================================================================

class Submission {
  final String studentName;
  final int daysLate;

  /// Çoktan seçmeli için işaretlenen şıklar.
  final List<String> choices;

  /// Yazılı/proje için metin.
  final String content;

  /// Öğretmenin rubrik puanları (kriter -> puan).
  final Map<String, int> rubricScores;

  const Submission({
    required this.studentName,
    this.daysLate = 0,
    this.choices = const [],
    this.content = '',
    this.rubricScores = const {},
  });

  bool get isEmpty => choices.isEmpty && content.trim().isEmpty;
  int get wordCount =>
      content.trim().isEmpty ? 0 : content.trim().split(RegExp(r'\s+')).length;
}

class GradeResult {
  final String studentName;
  final String assignmentTitle;
  final int rawScore;
  final int penaltyPercent;
  final int finalScore;
  final int maxPoints;
  final String feedback;

  const GradeResult({
    required this.studentName,
    required this.assignmentTitle,
    required this.rawScore,
    required this.penaltyPercent,
    required this.finalScore,
    required this.maxPoints,
    required this.feedback,
  });

  double get percentage => maxPoints == 0 ? 0 : (finalScore / maxPoints) * 100;

  @override
  String toString() {
    final ceza = penaltyPercent > 0 ? ' (-%$penaltyPercent geç)' : '';
    return '${studentName.padRight(14)} $finalScore/$maxPoints$ceza';
  }
}


// ############################################################################
//
//  BÖLÜM 1 — BASE SINIF
//
//  Base, üç override edilebilir metot sunuyor ve bunları evaluate()
//  içinde birleştiriyor. evaluate() kasıtlı olarak override edilmiyor:
//  değerlendirmenin ADIMLARI sabit, adımların İÇERİĞİ değişken.
//
// ############################################################################

abstract class Assignment {
  final String title;
  final int maxPoints;
  final DateTime dueDate;

  Assignment({
    required this.title,
    required this.maxPoints,
    required this.dueDate,
  }) {
    if (maxPoints < 1) {
      throw ArgumentError.value(maxPoints, 'maxPoints', 'En az 1 olmalı');
    }
  }

  /// Ödev türünün adı. Her alt sınıf kendi cevabını verir.
  String get kind;

  // ==========================================================================
  // OVERRIDE EDİLEBİLİR 1: HAM PUAN
  //
  // Varsayılan davranış: teslim edildiyse tam puan (katılım notu).
  // Bu, en basit ödev türü için doğru davranış. Daha karmaşık türler
  // bunu değiştirecek.
  // ==========================================================================
  int gradeSubmission(Submission submission) {
    return submission.isEmpty ? 0 : maxPoints;
  }

  // ==========================================================================
  // OVERRIDE EDİLEBİLİR 2: GECİKME CEZASI
  //
  // Varsayılan: her gün için %10, en fazla %100.
  // ==========================================================================
  int latePenaltyPercent(int daysLate) {
    if (daysLate <= 0) return 0;
    return _clamp(daysLate * 10, 0, 100);
  }

  // ==========================================================================
  // OVERRIDE EDİLEBİLİR 3: GERİ BİLDİRİM
  //
  // Varsayılan: yüzdeye göre genel bir cümle.
  // ==========================================================================
  String feedback(Submission submission, int finalScore) {
    final percent = (finalScore / maxPoints) * 100;
    if (percent >= 85) return 'Çok iyi.';
    if (percent >= 70) return 'İyi, birkaç eksik var.';
    if (percent >= 50) return 'Geçer, ama üzerinde çalışmalısın.';
    return 'Yetersiz. Konuyu tekrar et.';
  }

  bool get allowsResubmission => false;

  // ==========================================================================
  // TEMPLATE METHOD — BU OVERRIDE EDİLMİYOR
  //
  // Üç adımı sabit sırayla birleştiriyor. Alt sınıflar adımların
  // İÇERİĞİNİ değiştiriyor, SIRASINI değil.
  //
  // Bu satırların hangi alt sınıfla çalıştığından haberi yok. Yine de
  // her ödev türü için doğru hesabı yapıyor — dynamic dispatch sayesinde.
  // ==========================================================================
  GradeResult evaluate(Submission submission) {
    final raw = gradeSubmission(submission);
    final penalty = latePenaltyPercent(submission.daysLate);
    final afterPenalty = raw - ((raw * penalty) ~/ 100);
    final finalScore = _clamp(afterPenalty, 0, maxPoints);

    return GradeResult(
      studentName: submission.studentName,
      assignmentTitle: title,
      rawScore: raw,
      penaltyPercent: penalty,
      finalScore: finalScore,
      maxPoints: maxPoints,
      feedback: feedback(submission, finalScore),
    );
  }

  @override
  String toString() => '[$kind] $title ($maxPoints puan)';
}


// ############################################################################
//
//  BÖLÜM 2 — DÖRT OVERRIDE STRATEJİSİ
//
//  Override etmek tek bir şey değil. Dört farklı ilişki kurabilirsin:
//    A. TAM DEĞİŞTİRME  — super hiç çağrılmaz
//    B. GENİŞLETME      — super çağrılır, üstüne eklenir
//    C. KOŞULLU         — bazı durumlarda super, bazılarında kendi mantığı
//    D. HİÇ ETMEME      — base zaten doğru davranıyor
//
// ############################################################################


/// ---------------------------------------------------------------------------
/// STRATEJİ A + A: TAM DEĞİŞTİRME
/// ---------------------------------------------------------------------------
class MultipleChoiceAssignment extends Assignment {
  final List<String> answerKey;

  MultipleChoiceAssignment({
    required super.title,
    required super.dueDate,
    required this.answerKey,
  }) : super(maxPoints: answerKey.length * 10);

  @override
  String get kind => 'Test';

  /// NEDEN FARKLI DAVRANIŞ GEREKİYOR:
  /// Base "teslim edildiyse tam puan" diyor. Testte teslim etmek yetmez;
  /// cevapların doğruluğu ölçülür. Base'in mantığının hiçbir parçası
  /// burada işe yaramıyor, o yüzden super ÇAĞRILMIYOR — tam değiştirme.
  @override
  int gradeSubmission(Submission submission) {
    var correct = 0;
    for (var i = 0; i < answerKey.length; i++) {
      if (i < submission.choices.length &&
          submission.choices[i].toUpperCase() == answerKey[i].toUpperCase()) {
        correct++;
      }
    }
    return correct * 10;
  }

  /// NEDEN FARKLI DAVRANIŞ GEREKİYOR:
  /// Testte "hangi soruları yanlış yaptın" bilgisi genel bir cümleden
  /// çok daha yararlı. Base'in cümlesini eklemiyoruz çünkü soru
  /// listesinin yanında gereksiz gürültü olurdu.
  @override
  String feedback(Submission submission, int finalScore) {
    final yanlislar = <int>[];
    for (var i = 0; i < answerKey.length; i++) {
      final verilen =
          i < submission.choices.length ? submission.choices[i] : '(boş)';
      if (verilen.toUpperCase() != answerKey[i].toUpperCase()) {
        yanlislar.add(i + 1);
      }
    }
    if (yanlislar.isEmpty) return 'Tüm sorular doğru.';
    return 'Yanlış sorular: ${yanlislar.join(', ')}';
  }
}


/// ---------------------------------------------------------------------------
/// STRATEJİ A + B: TAM DEĞİŞTİRME + GENİŞLETME
/// ---------------------------------------------------------------------------
class EssayAssignment extends Assignment {
  final int minWords;
  final Map<String, int> rubric; // kriter -> maksimum puan

  EssayAssignment({
    required super.title,
    required super.dueDate,
    required this.minWords,
    required this.rubric,
  }) : super(maxPoints: rubric.values.fold(0, (a, b) => a + b));

  @override
  String get kind => 'Yazılı';

  /// NEDEN FARKLI: Yazılı, öğretmenin rubrik puanlarıyla değerlendirilir.
  /// Ayrıca kelime sınırının altındaki metinler kısmi puan alır.
  @override
  int gradeSubmission(Submission submission) {
    if (submission.isEmpty) return 0;

    var total = 0;
    rubric.forEach((kriter, maks) {
      final verilen = submission.rubricScores[kriter] ?? 0;
      total += _clamp(verilen, 0, maks);
    });

    // Kelime sınırı altındaysa oransal kesinti.
    if (submission.wordCount < minWords) {
      final oran = submission.wordCount / minWords;
      total = (total * oran).round();
    }
    return total;
  }

  /// NEDEN FARKLI: Yazılıda gecikme cezası günlük değil, sabit.
  /// Okul politikası: geç teslim edilen yazılı %25 kaybeder, kaç gün
  /// geç olduğu fark etmez. Base'in günlük mantığı burada geçersiz.
  @override
  int latePenaltyPercent(int daysLate) => daysLate > 0 ? 25 : 0;

  /// GENİŞLETME (strateji B):
  /// Base'in genel değerlendirmesi hâlâ değerli — onu alıyoruz ve
  /// üstüne rubrik dökümünü ekliyoruz. super ÇAĞRILIYOR.
  @override
  String feedback(Submission submission, int finalScore) {
    final genel = super.feedback(submission, finalScore);

    final dokum = rubric.keys.map((kriter) {
      final alinan = submission.rubricScores[kriter] ?? 0;
      return '$kriter ${alinan}/${rubric[kriter]}';
    }).join(' · ');

    final uyari = submission.wordCount < minWords
        ? ' [${submission.wordCount}/$minWords kelime]'
        : '';

    return '$genel  ($dokum)$uyari';
  }
}


/// ---------------------------------------------------------------------------
/// STRATEJİ C: KOŞULLU SUPER ÇAĞRISI
/// ---------------------------------------------------------------------------
class ProjectAssignment extends Assignment {
  final int gracePeriodDays;

  ProjectAssignment({
    required super.title,
    required super.maxPoints,
    required super.dueDate,
    this.gracePeriodDays = 3,
  });

  @override
  String get kind => 'Proje';

  /// NEDEN FARKLI: Projeler uzun sürer, teknik aksaklıklar olur.
  /// Okul 3 günlük tolerans tanıyor. Tolerans dolduktan SONRA ise
  /// standart günlük ceza geçerli.
  ///
  /// KOŞULLU SUPER: tolerans içindeyse kendi cevabımız (0), dışındaysa
  /// base'in mantığını kalan gün sayısıyla çağırıyoruz. Base'in ceza
  /// oranı yarın değişirse burası da otomatik uyum sağlar.
  @override
  int latePenaltyPercent(int daysLate) {
    if (daysLate <= gracePeriodDays) return 0;
    return super.latePenaltyPercent(daysLate - gracePeriodDays);
  }

  /// GENİŞLETME: base'in cümlesi + yeniden teslim hatırlatması.
  @override
  String feedback(Submission submission, int finalScore) {
    final base = super.feedback(submission, finalScore);
    if (finalScore < maxPoints * 0.7 && allowsResubmission) {
      return '$base Bir hafta içinde yeniden teslim edebilirsin.';
    }
    return base;
  }

  /// NEDEN FARKLI: Proje bir öğrenme sürecidir; tek denemede
  /// bitmesi beklenmez.
  @override
  bool get allowsResubmission => true;
}


/// ---------------------------------------------------------------------------
/// STRATEJİ D: HİÇ OVERRIDE ETMEMEK
///
/// Bu sınıf sadece 'kind' cevabını veriyor. gradeSubmission,
/// latePenaltyPercent ve feedback base'den olduğu gibi geliyor.
///
/// Override ETMEMEK de bilinçli bir karardır. Base'in davranışı zaten
/// doğruysa, "her alt sınıf bir şeyler override etmeli" diye bir kural
/// yok. Gereksiz override, base değiştiğinde kopan bir bağ demektir.
/// ---------------------------------------------------------------------------
class ParticipationAssignment extends Assignment {
  ParticipationAssignment({
    required super.title,
    required super.dueDate,
    super.maxPoints = 20,
  });

  @override
  String get kind => 'Katılım';
}


// ############################################################################
//
//  BÖLÜM 6 — KÖTÜ OVERRIDE (SÖZLEŞMEYİ BOZMAK)
//
//  Override, base'in SÖZ VERDİĞİ şeyi korumak zorundadır. Aşağıdaki
//  sınıf bunu yapmıyor:
//    - gradeSubmission maxPoints'i aşan puan döndürüyor
//    - latePenaltyPercent negatif dönerek cezayı bonusa çeviriyor
//
//  Derleyici bunu yakalayamaz; imzalar doğru. Bozulan şey imza değil,
//  ANLAM. Çağıran kod "puan 0 ile maxPoints arasındadır" varsayımıyla
//  yazıldığı için sessizce yanlış çalışır.
//
// ############################################################################

class BozukOdev extends Assignment {
  BozukOdev({required super.title, required super.dueDate})
      : super(maxPoints: 100);

  @override
  String get kind => 'Bozuk';

  @override
  int gradeSubmission(Submission submission) => 250; // maxPoints = 100

  @override
  int latePenaltyPercent(int daysLate) => -50; // ceza değil, bonus
}


// ============================================================================
// ÇALIŞTIRMA
// ============================================================================

void main() {
  final sonTeslim = DateTime(2026, 3, 15);

  // ==========================================================================
  print('=== BÖLÜM 1 + 2: DÖRT ÖDEV TÜRÜ ===');

  final test = MultipleChoiceAssignment(
    title: 'Üslü Sayılar Testi',
    dueDate: sonTeslim,
    answerKey: ['A', 'C', 'B', 'D', 'A'],
  );

  final yazili = EssayAssignment(
    title: 'Tanzimat Edebiyatı',
    dueDate: sonTeslim,
    minWords: 300,
    rubric: {'İçerik': 40, 'Dil': 30, 'Kaynak': 30},
  );

  final proje = ProjectAssignment(
    title: 'Köprü Maketi',
    maxPoints: 100,
    dueDate: sonTeslim,
  );

  final katilim = ParticipationAssignment(
    title: 'Sınıf Katılımı — Mart',
    dueDate: sonTeslim,
  );

  for (final o in [test, yazili, proje, katilim]) {
    print('  $o');
  }
  print('');


  // ==========================================================================
  print('=== BÖLÜM 2.1: AYNI METOT, FARKLI SONUÇ ===');

  print('  latePenaltyPercent(5) her türde farklı:');
  print('    Test    : %${test.latePenaltyPercent(5)}   (base: günlük %10)');
  print('    Yazılı  : %${yazili.latePenaltyPercent(5)}   (sabit %25)');
  print('    Proje   : %${proje.latePenaltyPercent(5)}   (3 gün tolerans, sonra %10/gün)');
  print('    Katılım : %${katilim.latePenaltyPercent(5)}   (base\'den, override yok)');
  print('');
  print('  Projenin tolerans mantığı super\'i çağırıyor:');
  for (final gun in [0, 2, 3, 4, 6, 15]) {
    print('    $gun gün geç -> %${proje.latePenaltyPercent(gun)}');
  }
  print('');


  // ==========================================================================
  print('=== BÖLÜM 3: SUBSTITUTION DEMO ===');
  print('  Liste tipi List<Assignment>. İçinde dört FARKLI sınıf var.');
  print('  Döngü hiçbirinin adını bilmiyor.');
  print('');

  final odevler = <Assignment>[test, yazili, proje, katilim];

  const teslim = Submission(
    studentName: 'Mehmet Demir',
    daysLate: 4,
    choices: ['A', 'C', 'D', 'D', 'B'],
    content:
        'Tanzimat dönemi Osmanlı edebiyatında Batı etkisinin belirginleştiği '
        'bir kırılma noktasıdır. Şinasi, Namık Kemal ve Ziya Paşa bu dönemin '
        'öncü isimleridir.',
    rubricScores: {'İçerik': 32, 'Dil': 25, 'Kaynak': 18},
  );

  for (final odev in odevler) {
    // Tek satır. Hangi sınıf olduğunu sormuyoruz. if/switch yok.
    final sonuc = odev.evaluate(teslim);
    print('  ${odev.kind.padRight(9)} ${odev.title}');
    print('     ham ${sonuc.rawScore} -> ceza %${sonuc.penaltyPercent} -> '
        'final ${sonuc.finalScore}/${sonuc.maxPoints}');
    print('     "${sonuc.feedback}"');
  }
  print('');

  print('  POLYMORPHISM BUDUR: tek bir tip (Assignment) üzerinden');
  print('  konuşuyoruz, çalışan kod her seferinde farklı.');
  print('');

  print('  --- if/switch ile yazsaydık ---');
  print('  for (final odev in odevler) {');
  print('    if (odev is MultipleChoiceAssignment) { ... }');
  print('    else if (odev is EssayAssignment) { ... }');
  print('    else if (odev is ProjectAssignment) { ... }');
  print('  }');
  print('  Yeni bir ödev türü eklendiğinde bu bloğu bulup güncellemen');
  print('  gerekirdi — hem burada, hem aynı deseni kullanan her yerde.');
  print('  Polymorphism\'de yeni tür eklemek mevcut kodun hiçbirine');
  print('  dokunmaz. (Bu, Open/Closed Principle olarak karşına çıkacak.)');
  print('');


  // ==========================================================================
  print('=== BÖLÜM 3.1: TOPLU İŞLEM ===');

  const teslimler = [
    Submission(
      studentName: 'Ayşe Kaya',
      choices: ['A', 'C', 'B', 'D', 'A'],
      content: 'Kısa bir metin.',
      rubricScores: {'İçerik': 38, 'Dil': 28, 'Kaynak': 27},
    ),
    Submission(
      studentName: 'Mert Aslan',
      daysLate: 2,
      choices: ['A', 'B', 'B', 'D', 'C'],
      content: 'Orta uzunlukta bir cevap metni buraya geliyor.',
      rubricScores: {'İçerik': 25, 'Dil': 20, 'Kaynak': 15},
    ),
    Submission(studentName: 'Boş Teslim', daysLate: 1),
  ];

  for (final odev in odevler) {
    print('  ${odev.title}:');
    final sonuclar = teslimler.map(odev.evaluate).toList();
    for (final s in sonuclar) {
      print('    $s');
    }
    final ortalama =
        sonuclar.map((s) => s.percentage).reduce((a, b) => a + b) /
            sonuclar.length;
    print('    sınıf ortalaması: %${ortalama.toStringAsFixed(1)}');
  }
  print('');


  // ==========================================================================
  print('=== BÖLÜM 4: DYNAMIC DISPATCH ===');

  // Değişkenin STATİK tipi Assignment. Derleyici sadece bunu biliyor.
  Assignment odev = yazili;

  print('  Değişkenin bildirilen tipi : Assignment');
  print('  Çalışma anındaki gerçek tip: ${odev.runtimeType}');
  print('  odev.latePenaltyPercent(5) -> %${odev.latePenaltyPercent(5)}');
  print('  Yazılı\'nın metodu çalıştı, base\'inki değil.');
  print('');

  odev = proje;
  print('  Aynı değişkene proje atadık:');
  print('  odev.latePenaltyPercent(5) -> %${odev.latePenaltyPercent(5)}');
  print('');
  print('  Kural: metot seçimi DERLEME anında değil, ÇALIŞMA anında');
  print('  nesnenin gerçek tipine bakılarak yapılır. Buna dynamic');
  print('  dispatch (ya da geç bağlama) denir.');
  print('');
  print('  Gün 11\'deki tuzağın sebebi de buydu: base constructor\'ı');
  print('  çalışırken bile alt sınıfın override\'ı devreye giriyor,');
  print('  ama alt sınıfın alanları henüz hazır değil.');
  print('');


  // ==========================================================================
  print('=== BÖLÜM 5: OVERRIDE EDİLMEYENLER ===');

  print('  1. static metotlar override edilmez.');
  print('     Statik üyeler sınıfa aittir, nesneye değil; dynamic');
  print('     dispatch\'e girmezler.');
  print('');
  print('  2. @override sadece bir NOTTUR, zorunlu değildir —');
  print('     ama mutlaka yaz. Metot adını yanlış yazarsan');
  print('     (feedbak gibi) @override olmadan derleyici susar ve');
  print('     yeni bir metot tanımlamış olursun. Base\'inki çağrılmaya');
  print('     devam eder, sen neden çalışmadığını ararsın.');
  print('     @override varsa derleyici hemen "böyle bir üye yok" der.');
  print('');
  print('  3. Alan (field) override etme. Dart izin verse de');
  print('     kafa karıştırır; getter override et.');
  print('');


  // ==========================================================================
  print('=== BÖLÜM 6: KÖTÜ OVERRIDE ===');

  final bozuk = BozukOdev(title: 'Sözleşme İhlali', dueDate: sonTeslim);
  const basitTeslim = Submission(studentName: 'Test Öğrenci', daysLate: 2);

  final bozukSonuc = bozuk.evaluate(basitTeslim);
  print('  maxPoints: ${bozuk.maxPoints}');
  print('  ham puan : ${bozukSonuc.rawScore}   <- maxPoints\'i aşıyor');
  print('  ceza     : %${bozukSonuc.penaltyPercent}   <- negatif, yani bonus');
  print('  final    : ${bozukSonuc.finalScore}   (evaluate clamp\'ledi, şans eseri)');
  print('');
  print('  İmzalar doğru, derleyici memnun. Bozulan şey imza değil ANLAM.');
  print('  Çağıran kod "puan 0..maxPoints arasındadır" varsayımıyla');
  print('  yazılmıştı; bu override o sözü tutmuyor.');
  print('');
  print('  --- İYİ OVERRIDE\'IN ÜÇ KURALI ---');
  print('  1. Base\'in söz verdiği aralığı/tipi koru.');
  print('     Base 0..100 dönüyorsa sen de 0..100 dön.');
  print('  2. Base\'in kabul ettiği girdiyi reddetme.');
  print('     Base her sayıyı kabul ediyorsa sen negatifi reddetme.');
  print('  3. Base\'in fırlatmadığı hatayı fırlatma.');
  print('     "Bu metodu desteklemiyorum" diyen bir override,');
  print('     o kalıtımın yanlış olduğunun işaretidir.');
  print('');
  print('  Bu üç kural birlikte Liskov Substitution Principle\'ı oluşturur:');
  print('  alt sınıf, base\'in gittiği her yere sorunsuz gidebilmelidir.');
}