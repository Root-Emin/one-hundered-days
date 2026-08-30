// ============================================================================
// GÜN 14 — COMPOSITION OVER INHERITANCE  (Dart)
//
// Çalıştırmak için:  dart run gun14_composition_over_inheritance.dart
// veya dartpad.dev'e yapıştır.
//
// BÖLÜM 1 -> Zorlanmış hiyerarşi: sınıf patlaması
// BÖLÜM 2 -> Kötü IS-A: base'in API'sinin sızması
// BÖLÜM 3 -> Delegation: kalıtım yerine alan
// BÖLÜM 4 -> Strategy: davranışı dışarıdan vermek
// BÖLÜM 5 -> Sarmalayıcılar: çalışma anında kombinasyon
// BÖLÜM 6 -> Kalıtımın YAPAMADIĞI şey
// BÖLÜM 7 -> Kalıtımın doğru olduğu yer
// BÖLÜM 8 -> Rule of thumb
// ============================================================================

// ============================================================================
// TEMEL VERİ NESNELERİ
// ============================================================================

enum Priority { normal, urgent }

class Message {
  final String recipientName;
  final String address;
  final String subject;
  final String body;
  final Priority priority;

  const Message({
    required this.recipientName,
    required this.address,
    required this.subject,
    required this.body,
    this.priority = Priority.normal,
  });
}

class SendResult {
  final bool ok;
  final String channel;
  final String detail;
  final int attempts;

  const SendResult({
    required this.ok,
    required this.channel,
    required this.detail,
    this.attempts = 1,
  });

  SendResult withAttempts(int n) =>
      SendResult(ok: ok, channel: channel, detail: detail, attempts: n);

  @override
  String toString() {
    final deneme = attempts > 1 ? ' [$attempts deneme]' : '';
    return '${ok ? "✓" : "✗"} $channel: $detail$deneme';
  }
}

// ############################################################################
//
//  BÖLÜM 1 — ZORLANMIŞ HİYERARŞİ (SINIF PATLAMASI)
//
//  Bir bildirim sisteminde dört bağımsız değişken var:
//    kanal      : e-posta, SMS, push          (3)
//    biçim      : düz, şablonlu, kısa         (3)
//    tekrar     : var, yok                    (2)
//    kayıt      : var, yok                    (2)
//
//  Bunları KALITIMLA modellersen her kombinasyon ayrı sınıf olur:
//
//    EmailNotifier
//    RetryingEmailNotifier
//    LoggingEmailNotifier
//    RetryingLoggingEmailNotifier
//    TemplatedRetryingLoggingEmailNotifier
//    ... ve SMS için aynısı, push için aynısı
//
//  3 × 3 × 2 × 2 = 36 sınıf. Beşinci bir boyut eklersen (örneğin
//  hız sınırı) sayı ikiye katlanır.
//
//  KALITIMIN BURADA NEDEN YANLIŞ OLDUĞU:
//
//  1. Kombinasyon patlaması: her yeni boyut sınıf sayısını çarpar.
//  2. Kod tekrarı: RetryingEmailNotifier ile RetryingSmsNotifier'daki
//     tekrar mantığı birebir aynı ama paylaşılamıyor — çünkü ikisi
//     farklı base'lerden geliyor.
//  3. Çalışma anında değiştirilemez: kullanıcı ayarlardan "tekrar
//     denemeyi kapat" derse yeni bir NESNE üretmen gerekir.
//  4. Tek kalıtım sınırı: "hem tekrar deneyen hem loglayan" için iki
//     base'den birden extends edemezsin.
//  5. İsimlendirme çöküyor: TemplatedRetryingThrottledLoggingSmsNotifier.
//  6. Test etmesi zor: tekrar mantığını tek başına test edemezsin,
//     her zaman bir kanalla birlikte gelir.
//
//  Aşağıda AYNI işi 3 + 3 + 3 = 9 küçük sınıfla, hem de daha fazla
//  kombinasyon üretebilecek şekilde yapıyoruz.
//
// ############################################################################

// ############################################################################
//
//  BÖLÜM 2 — KÖTÜ IS-A: BASE'İN API'Sİ SIZIYOR
//
//  "Stack bir listedir" cümlesi kulağa doğru geliyor. Değil.
//  Stack SADECE iki işlem sunar: push ve pop. Listeden extends edersen
//  listenin BÜTÜN yetenekleri de dışarı açılır ve LIFO garantisi çöker.
//
// ############################################################################

class SimpleCollection<T> {
  final List<T> items = [];

  void add(T item) => items.add(item);
  void insertAt(int index, T item) => items.insert(index, item);
  T removeAt(int index) => items.removeAt(index);
  T removeLast() => items.removeLast();
  int get length => items.length;
}

/// KÖTÜ: extends ile base'in her şeyi miras alındı.
class BadStack<T> extends SimpleCollection<T> {
  void push(T item) => add(item);
  T pop() => removeLast();
  // Ama insertAt, removeAt ve items de dışarıda. LIFO sözü tutulamıyor.
}

/// İYİ: listeyi İÇİNDE tutuyor (has-a), sadece istediğini dışarı açıyor.
class GoodStack<T> {
  final List<T> _items = [];

  void push(T item) => _items.add(item);

  T pop() {
    if (_items.isEmpty) throw StateError('Stack boş');
    return _items.removeLast();
  }

  T get top {
    if (_items.isEmpty) throw StateError('Stack boş');
    return _items.last;
  }

  int get length => _items.length;
  bool get isEmpty => _items.isEmpty;

  @override
  String toString() => _items.toString();
}

// ############################################################################
//
//  BÖLÜM 3 + 4 — SÖZLEŞMELER VE PARÇALAR
//
// ############################################################################

/// Tek bir sözleşme. Hem gerçek kanallar hem sarmalayıcılar bunu uyguluyor.
/// Sarmalayıcıların da aynı tipte olması, Bölüm 5'teki zincirlemeyi
/// mümkün kılan şey.
abstract interface class MessageSender {
  Future<SendResult> send(Message message);
  String get name;
}

/// STRATEGY: metnin nasıl biçimleneceği. Kanaldan bağımsız bir karar.
abstract interface class MessageFormatter {
  String format(Message message);
  String get name;
}

class PlainFormatter implements MessageFormatter {
  @override
  String get name => 'düz';

  @override
  String format(Message m) => m.body;
}

class TemplateFormatter implements MessageFormatter {
  @override
  String get name => 'şablonlu';

  @override
  String format(Message m) {
    final aciliyet = m.priority == Priority.urgent ? '[ACİL] ' : '';
    return '${aciliyet}Sayın ${m.recipientName},\n\n${m.body}\n\nOkul Yönetimi';
  }
}

class ShortFormatter implements MessageFormatter {
  final int maxLength;
  const ShortFormatter({this.maxLength = 160});

  @override
  String get name => 'kısa';

  @override
  String format(Message m) {
    final tam = '${m.subject}: ${m.body}';
    if (tam.length <= maxLength) return tam;
    return '${tam.substring(0, maxLength - 3)}...';
  }
}

// ############################################################################
//
//  KANALLAR — HER BİRİ BİÇİMLEYİCİYİ DELEGE EDİYOR
//
//  Dikkat: hiçbiri ortak bir base'den extends etmiyor. Biçimleme
//  davranışını miras almıyorlar, İÇLERİNDE TUTUYORLAR (has-a).
//
//  Kazanç: aynı EmailSender sınıfı üç farklı biçimleyiciyle çalışabiliyor.
//  Kalıtımla bu üç ayrı sınıf demek olurdu.
//
// ############################################################################

class EmailSender implements MessageSender {
  final MessageFormatter _formatter; // DELEGATION
  final List<String> outbox = [];

  EmailSender({MessageFormatter? formatter})
    : _formatter = formatter ?? TemplateFormatter();

  @override
  String get name => 'E-posta(${_formatter.name})';

  @override
  Future<SendResult> send(Message message) async {
    final metin = _formatter.format(message); // işi delege ediyoruz
    outbox.add(metin);
    return SendResult(
      ok: true,
      channel: name,
      detail: '${message.address} — ${metin.length} karakter',
    );
  }
}

class SmsSender implements MessageSender {
  final MessageFormatter _formatter;
  final List<String> outbox = [];

  SmsSender({MessageFormatter? formatter})
    : _formatter = formatter ?? const ShortFormatter();

  @override
  String get name => 'SMS(${_formatter.name})';

  @override
  Future<SendResult> send(Message message) async {
    final metin = _formatter.format(message);
    if (metin.length > 160) {
      return SendResult(
        ok: false,
        channel: name,
        detail: 'Mesaj çok uzun (${metin.length} > 160)',
      );
    }
    outbox.add(metin);
    return SendResult(ok: true, channel: name, detail: message.address);
  }
}

/// Test için: ilk N denemede hata veren kanal.
class FlakySender implements MessageSender {
  int _failuresLeft;
  final String _label;

  FlakySender({int failures = 2, String label = 'Push'})
    : _failuresLeft = failures,
      _label = label;

  @override
  String get name => _label;

  @override
  Future<SendResult> send(Message message) async {
    if (_failuresLeft > 0) {
      _failuresLeft--;
      return SendResult(ok: false, channel: name, detail: 'Bağlantı hatası');
    }
    return SendResult(ok: true, channel: name, detail: message.address);
  }
}

// ############################################################################
//
//  BÖLÜM 5 — SARMALAYICILAR (WRAPPER / DECORATOR)
//
//  Her biri MessageSender'ı hem UYGULUYOR hem İÇİNDE TUTUYOR.
//  Bu ikisi bir arada olunca sınırsız zincir kurulabiliyor:
//
//     Logging( Retrying( Throttled( EmailSender() ) ) )
//
//  Kalıtımla bu zincir mümkün değil — her sıralama ayrı bir sınıf
//  olurdu ve sıralamayı çalışma anında değiştiremezdin.
//
// ############################################################################

class RetryingSender implements MessageSender {
  final MessageSender _inner;
  final int maxAttempts;

  const RetryingSender(this._inner, {this.maxAttempts = 3});

  @override
  String get name => 'Retry(${_inner.name})';

  @override
  Future<SendResult> send(Message message) async {
    SendResult? son;
    for (var deneme = 1; deneme <= maxAttempts; deneme++) {
      son = await _inner.send(message);
      if (son.ok) return son.withAttempts(deneme);
    }
    return son!.withAttempts(maxAttempts);
  }
}

class LoggingSender implements MessageSender {
  final MessageSender _inner;
  final List<String> log = [];

  LoggingSender(this._inner);

  @override
  String get name => 'Log(${_inner.name})';

  @override
  Future<SendResult> send(Message message) async {
    log.add('GÖNDERİLİYOR -> ${message.address} (${_inner.name})');
    final sonuc = await _inner.send(message);
    log.add('SONUÇ -> ${sonuc.ok ? "başarılı" : "başarısız"}: ${sonuc.detail}');
    return sonuc;
  }
}

class ThrottledSender implements MessageSender {
  final MessageSender _inner;
  final int limit;
  int _sent = 0;

  ThrottledSender(this._inner, {this.limit = 2});

  @override
  String get name => 'Throttle(${_inner.name})';

  @override
  Future<SendResult> send(Message message) async {
    if (_sent >= limit) {
      return SendResult(
        ok: false,
        channel: name,
        detail: 'Hız sınırı aşıldı ($limit mesaj)',
      );
    }
    _sent++;
    return _inner.send(message);
  }
}

/// Birincisi başarısız olursa ikincisini dener.
class FallbackSender implements MessageSender {
  final MessageSender _primary;
  final MessageSender _backup;

  const FallbackSender(this._primary, this._backup);

  @override
  String get name => '${_primary.name}→${_backup.name}';

  @override
  Future<SendResult> send(Message message) async {
    final ilk = await _primary.send(message);
    if (ilk.ok) return ilk;
    return _backup.send(message);
  }
}

/// Önceliğe göre farklı kanala yönlendirir.
class PriorityRouter implements MessageSender {
  final MessageSender _urgent;
  final MessageSender _normal;

  const PriorityRouter({
    required MessageSender urgent,
    required MessageSender normal,
  }) : _urgent = urgent,
       _normal = normal;

  @override
  String get name => 'Router';

  @override
  Future<SendResult> send(Message message) =>
      message.priority == Priority.urgent
      ? _urgent.send(message)
      : _normal.send(message);
}

// ############################################################################
//
//  BÖLÜM 6 — ÇALIŞMA ANINDA DEĞİŞTİRME
//
//  Kalıtımla bir nesnenin davranışı doğduğu anda sabitlenir.
//  Kompozisyonda strateji bir ALANDIR; alan değişebilir.
//
// ############################################################################

class NotificationService {
  MessageSender _sender; // final değil: değiştirilebilir

  NotificationService(this._sender);

  String get currentSetup => _sender.name;

  /// Kullanıcı ayarlardan kanalı değiştirdi. Yeni nesne üretmeye gerek yok.
  void switchTo(MessageSender sender) => _sender = sender;

  Future<SendResult> notify(Message message) => _sender.send(message);
}

// ############################################################################
//
//  BÖLÜM 7 — KALITIMIN DOĞRU OLDUĞU YER
//
//  Kompozisyon her şeyin cevabı değil. Aşağıdaki üçlü gerçek bir
//  IS-A ilişkisi:
//    - Hepsi aynı ailenin üyesi
//    - Hepsi ortak veriyi ve ortak kodu paylaşıyor
//    - Hiçbiri base'in bir sözünü reddetmiyor
//    - Hiyerarşi tek seviye
//
//  Böyle bir durumda kompozisyona zorlamak gereksiz tören olur.
//
// ############################################################################

abstract class SchoolEvent {
  final String title;
  final DateTime date;

  SchoolEvent({required this.title, required this.date});

  /// Ortak kod: her etkinlik aynı biçimde duyurulur.
  String announcement() =>
      '${_formatDate(date)} — $title ($audience için, $durationLabel)';

  String get audience;
  String get durationLabel;

  static String _formatDate(DateTime d) =>
      '${d.day.toString().padLeft(2, '0')}.${d.month.toString().padLeft(2, '0')}';
}

class ParentMeeting extends SchoolEvent {
  ParentMeeting({required super.date}) : super(title: 'Veli Toplantısı');

  @override
  String get audience => 'veliler';

  @override
  String get durationLabel => '2 saat';
}

class ExamDay extends SchoolEvent {
  final String subject;

  ExamDay({required this.subject, required super.date})
    : super(title: '$subject Sınavı');

  @override
  String get audience => 'öğrenciler';

  @override
  String get durationLabel => '40 dakika';
}

// ============================================================================
// ÇALIŞTIRMA
// ============================================================================

const _mesaj = Message(
  recipientName: 'Ayşe Kaya',
  address: 'ayse@example.com',
  subject: 'Devamsızlık Bildirimi',
  body: 'Öğrencinizin bugün 2 ders devamsızlığı bulunmaktadır.',
);

const _acil = Message(
  recipientName: 'Mert Aslan',
  address: '+90 555 000 00 00',
  subject: 'Acil',
  body: 'Okul erken tatil edildi, öğrencileri alınız.',
  priority: Priority.urgent,
);

Future<void> main() async {
  // ==========================================================================
  print('=== BÖLÜM 1: SINIF PATLAMASI ===');

  const kanal = 3, bicim = 3, tekrar = 2, kayit = 2;
  print(
    '  Boyutlar: kanal($kanal) × biçim($bicim) × tekrar($tekrar) × kayıt($kayit)',
  );
  print('  Kalıtımla gereken sınıf sayısı: ${kanal * bicim * tekrar * kayit}');
  print(
    '  Beşinci boyut (hız sınırı) eklenirse: ${kanal * bicim * tekrar * kayit * 2}',
  );
  print('');
  print('  Kompozisyonla: 3 kanal + 3 biçimleyici + 4 sarmalayıcı = 10 sınıf');
  print('  ve bunlarla 36\'dan çok daha fazla kombinasyon kurulabiliyor.');
  print('');
  print('  Kalıtımın buradaki altı sorunu:');
  const sorunlar = [
    'Her yeni boyut sınıf sayısını çarpıyor',
    'Aynı tekrar mantığı her kanalda yeniden yazılıyor',
    'Davranış çalışma anında değiştirilemiyor',
    'Tek kalıtım sınırı: iki base\'den birden miras alınamıyor',
    'İsimler okunamaz hale geliyor',
    'Tekrar mantığı tek başına test edilemiyor',
  ];
  for (var i = 0; i < sorunlar.length; i++) {
    print('    ${i + 1}. ${sorunlar[i]}');
  }
  print('');

  // ==========================================================================
  print('=== BÖLÜM 2: BASE\'İN API\'Sİ SIZIYOR ===');

  final kotu = BadStack<String>();
  kotu.push('a');
  kotu.push('b');
  kotu.push('c');
  print('  BadStack (extends SimpleCollection): $kotu içerik ${kotu.items}');

  // Bunlar bir stack'te OLMAMASI gereken işlemler, ama miras geldi:
  kotu.insertAt(0, 'kaçak');
  kotu.removeAt(1);
  kotu.items.clear();

  print('  insertAt(0,...), removeAt(1), items.clear() hepsi çalıştı.');
  print('  Son durum: ${kotu.items}  <- LIFO garantisi diye bir şey kalmadı');
  print('');

  final iyi = GoodStack<String>();
  iyi.push('a');
  iyi.push('b');
  iyi.push('c');
  print('  GoodStack (has-a): $iyi, tepe: ${iyi.top}');
  print('  iyi.insertAt(0, "kaçak");  <-- DERLENMEZ, böyle bir metot yok');
  print('  iyi.pop() -> ${iyi.pop()}, kalan: $iyi');
  print('');
  print('  extends: base\'in HER ŞEYİ dışarı açılır, seçme şansın yok.');
  print('  has-a  : sadece istediğin metotları dışarı açarsın.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 3 + 4: DELEGATION VE STRATEGY ===');
  print('  TEK bir EmailSender sınıfı, üç farklı biçimleyiciyle:');
  print('');

  final bicimleyiciler = <MessageFormatter>[
    PlainFormatter(),
    TemplateFormatter(),
    const ShortFormatter(maxLength: 80),
  ];

  for (final f in bicimleyiciler) {
    final sender = EmailSender(formatter: f);
    await sender.send(_mesaj);
    print('  [${f.name}]');
    for (final satir in sender.outbox.first.split('\n')) {
      print('    $satir');
    }
    print('');
  }

  print('  EmailSender biçimleme kodunu MİRAS ALMIYOR, delege ediyor.');
  print('  Kalıtımla bu üç ayrı sınıf olurdu: PlainEmailSender,');
  print('  TemplatedEmailSender, ShortEmailSender.');
  print('');
  print('  Aynı biçimleyiciler SMS ile de çalışıyor — kod tekrarı yok:');
  final sms = SmsSender(formatter: const ShortFormatter());
  print('  ${await sms.send(_mesaj)}');
  print('    "${sms.outbox.first}"');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 5: SARMALAYICILARLA KOMBİNASYON ===');

  print('  --- Sarmalayıcısız (2 kez hata verecek kanal) ---');
  final ciplak = FlakySender(failures: 2);
  print('  ${await ciplak.send(_mesaj)}');
  print('');

  print('  --- Retry ile sarmalanmış ---');
  final tekrarli = RetryingSender(FlakySender(failures: 2), maxAttempts: 4);
  print('  ${await tekrarli.send(_mesaj)}');
  print('');

  print('  --- Log(Retry(Flaky)) — üç katman ---');
  final kayitli = LoggingSender(
    RetryingSender(FlakySender(failures: 1), maxAttempts: 3),
  );
  print('  ${await kayitli.send(_mesaj)}');
  for (final satir in kayitli.log) {
    print('    $satir');
  }
  print('');

  print('  --- Throttle: ilk 2 mesaj geçer, üçüncü reddedilir ---');
  final sinirli = ThrottledSender(EmailSender(), limit: 2);
  for (var i = 1; i <= 3; i++) {
    print('    $i. ${await sinirli.send(_mesaj)}');
  }
  print('');

  print('  --- Fallback: SMS başarısız olursa e-postaya düş ---');
  final yedekli = FallbackSender(
    SmsSender(formatter: PlainFormatter()), // uzun metin -> 160\'ı aşacak
    EmailSender(),
  );
  print('    ${await yedekli.send(_mesaj)}');
  print('');

  print('  --- Router: acil olan SMS\'e, normal olan e-postaya ---');
  final router = PriorityRouter(
    urgent: RetryingSender(SmsSender()),
    normal: EmailSender(),
  );
  print('    normal -> ${await router.send(_mesaj)}');
  print('    acil   -> ${await router.send(_acil)}');
  print('');

  print('  Bu altı kurulumun hepsi AYNI on sınıftan üretildi.');
  print('  Yeni bir sarmalayıcı (şifreleme, imza, kuyruk) eklemek');
  print('  mevcut sınıfların hiçbirine dokunmuyor.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 6: ÇALIŞMA ANINDA DEĞİŞTİRME ===');

  final servis = NotificationService(EmailSender());
  print('  Başlangıç: ${servis.currentSetup}');
  print('    ${await servis.notify(_mesaj)}');

  // Kullanıcı ayarlardan "acil bildirimleri SMS ile al" dedi.
  servis.switchTo(RetryingSender(SmsSender()));
  print('  Ayar değişti: ${servis.currentSetup}');
  print('    ${await servis.notify(_acil)}');

  print('');
  print('  Nesne aynı nesne, davranış değişti.');
  print('  Kalıtımda bir nesnenin sınıfı doğduğu anda sabitlenir;');
  print('  davranışı değiştirmek için nesneyi ATMAK ve yenisini');
  print('  üretmek gerekir. Kompozisyonda strateji bir alandır.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 7: KALITIMIN DOĞRU OLDUĞU YER ===');

  final etkinlikler = <SchoolEvent>[
    ParentMeeting(date: DateTime(2026, 4, 10)),
    ExamDay(subject: 'Matematik', date: DateTime(2026, 4, 12)),
    ExamDay(subject: 'Tarih', date: DateTime(2026, 4, 14)),
  ];

  for (final e in etkinlikler) {
    print('  ${e.announcement()}');
  }

  print('');
  print('  Burada kalıtım doğru, çünkü:');
  print('    - "Sınav günü BİR okul etkinliğidir" doğru bir cümle');
  print('    - Hepsi aynı title/date verisini ve announcement() kodunu');
  print('      paylaşıyor');
  print('    - Hiçbiri base\'in bir sözünü reddetmiyor');
  print('    - Hiyerarşi tek seviye');
  print('  Böyle bir durumda kompozisyona zorlamak gereksiz tören olur.');
  print('');

  // ==========================================================================
  print('=== BÖLÜM 8: RULE OF THUMB ===');

  const kurallar = [
    ['Sadece kodu tekrar kullanmak istiyorum', 'KOMPOZİSYON'],
    ['"X BİR Y\'dir" cümlesi tuhaf geliyor', 'KOMPOZİSYON'],
    ['Birden fazla boyut/özellik birleşiyor', 'KOMPOZİSYON'],
    ['Davranış çalışma anında değişebilmeli', 'KOMPOZİSYON'],
    ['Base\'in bazı metotlarını gizlemek istiyorum', 'KOMPOZİSYON'],
    ['Base\'in kodunu değiştiremiyorum (3. parti)', 'KOMPOZİSYON'],
    ['Parçaları ayrı ayrı test etmek istiyorum', 'KOMPOZİSYON'],
    ['Gerçek bir alt tür ve ortak kod var', 'KALITIM'],
    ['Alt tür base\'in gittiği her yere gidebilir', 'KALITIM'],
    ['Tek seviyelik, kapalı bir aile', 'KALITIM'],
  ];

  print('  ${'DURUM'.padRight(46)}SEÇİM');
  print('  ${'-' * 46}${'-' * 14}');
  for (final k in kurallar) {
    print('  ${k[0].padRight(46)}${k[1]}');
  }

  print('');
  print('  Tek cümlelik özet:');
  print('    Kalıtım "NE OLDUĞU" içindir, kompozisyon "NE YAPTIĞI" için.');
  print('');
  print('  Üç soruluk pratik test:');
  print('    1. Cümleyi kur: "X bir Y\'dir" mi, "X\'in Y\'si vardır" mı?');
  print('    2. Base\'in metotlarından herhangi birini gizlemek istiyor');
  print('       musun? İstiyorsan kalıtım yanlış.');
  print('    3. İkinci bir boyut eklendiğinde kaç sınıf gerekecek?');
  print('       Çarpım çıkıyorsa kompozisyona geç.');
  print('');
  print('  Varsayılan: KOMPOZİSYONLA BAŞLA. Kalıtım geri alması pahalı');
  print('  bir karardır — bir kez hiyerarşi kurulunca ona bağlanan her');
  print('  yeri değiştirmeden geri dönemezsin.');
}
