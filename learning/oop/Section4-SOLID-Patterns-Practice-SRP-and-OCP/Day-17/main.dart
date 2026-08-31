// ============================================================================
// GÜN 17 — SOLID: LSP, ISP ve DIP  (Dart)
//
// Çalıştırmak için:  dart run gun17_lsp_isp_dip.dart
// veya dartpad.dev'e yapıştır.
//
// BÖLÜM 1 -> LSP Check      : alt tip, üst tipin yerine sorunsuz geçebiliyor mu?
// BÖLÜM 2 -> ISP Split      : şişman interface'i küçük sözleşmelere bölmek
// BÖLÜM 3 -> DIP Wire       : yüksek seviye politika, soyutlamaya bağlansın
// BÖLÜM 4 -> Violation Hunt : her ilkeden bir ihlal + düzeltme, mini testlerle
//
// NOT: 'abstract interface class' Dart 3 sözdizimidir. SDK'n eskiyse sadece
// 'abstract class' yaz; anlam bu dosya için aynı kalır.
// ============================================================================

// ============================================================================
// BÖLÜM 1 — LSP CHECK (Liskov Substitution Principle)
//
// Kural: B, A'nın alt tipiyse; A bekleyen HER kod, B verildiğinde de
// çalışmaya devam etmeli. Çağıran kodun "acaba elimdeki gerçekte hangi
// alt tip?" diye sormak zorunda kalması = LSP ihlali.
//
// İhlalin üç klasik biçimi:
//   1) Ön koşulu güçlendirmek  (alt tip daha az girdi kabul eder)   <- 1A
//   2) Son koşulu zayıflatmak  (alt tip daha az garanti verir)
//   3) Mirası reddetmek        (alt tip metodu UnsupportedError atar) <- 1C
// ============================================================================

// ---------------------------------------------------------------- 1A: İHLAL
// Üst tipin sözleşmesi: "save(key, bytes) verilen byte'ları saklar."
// Boyut sınırından hiç söz edilmiyor -> çağıran "her boyut olur" varsayar.
class ImageStore {
  final Map<String, List<int>> _files = {};

  void save(String key, List<int> bytes) {
    _files[key] = bytes;
  }

  int get count => _files.length;
  bool has(String key) => _files.containsKey(key);
}

// Alt tip ÖN KOŞULU GÜÇLENDİRİYOR: "ama 1 KB'den büyük olmayacak".
// Üst tipin sözleşmesinde olmayan yeni bir şart ekledi -> LSP ihlali.
class ThumbnailStoreBad extends ImageStore {
  static const int _maxBytes = 1024;

  @override
  void save(String key, List<int> bytes) {
    if (bytes.length > _maxBytes) {
      throw ArgumentError('Thumbnail 1 KB üstü olamaz (${bytes.length} B)');
    }
    super.save(key, bytes);
  }
}

// Çağıran kod ImageStore sözleşmesine göre yazıldı; ThumbnailStoreBad'i
// hiç duymadı bile. Yine de onun yüzünden patlıyor.
void uploadAvatar(ImageStore store, String userId, List<int> bytes) {
  store.save('avatar/$userId', bytes);
}

// ------------------------------------------------------------- 1B: DÜZELTME
// Çözüm: sınırı GİZLİ bir sürpriz olmaktan çıkarıp SÖZLEŞMENİN PARÇASI yap.
// Artık ön koşul tüm alt tiplerde aynı cümle: "accepts(bytes) true olmalı".
// Değişen şey sadece sözleşmenin ilan ettiği bir parametre: maxBytes.
abstract class SizedImageStore {
  final Map<String, List<int>> _files = {};

  /// Sözleşmenin parçası: her deponun bir üst sınırı vardır.
  int get maxBytes;

  /// Çağıran, save'den ÖNCE bunu sorabilir. Sorgu (query), komut değil.
  bool accepts(List<int> bytes) => bytes.length <= maxBytes;

  /// Ön koşul: accepts(bytes) == true. Bu cümle her alt tip için geçerli.
  void save(String key, List<int> bytes) {
    if (!accepts(bytes)) {
      throw ArgumentError(
        'accepts() false iken save() çağrıldı: ${bytes.length} B > $maxBytes B',
      );
    }
    _files[key] = bytes;
  }

  int get count => _files.length;
}

class DiskImageStore extends SizedImageStore {
  @override
  int get maxBytes => 10 * 1024 * 1024; // 10 MB
}

class ThumbnailStore extends SizedImageStore {
  @override
  int get maxBytes => 1024; // 1 KB
}

/// Çağıran artık hangi alt tip olduğunu bilmek zorunda değil.
/// Sözleşmeye uyuyor: önce sor, sonra yaz.
bool uploadAvatarSafely(SizedImageStore store, String userId, List<int> bytes) {
  if (!store.accepts(bytes)) return false;
  store.save('avatar/$userId', bytes);
  return true;
}

// ------------------------------------- 1C: MİRASI REDDETME (refused bequest)
// "Arşiv listesi de bir listedir" diye extends ettik; sonra add()'i iptal ettik.
class TaskListBad {
  final List<String> _items = [];
  void add(String title) => _items.add(title);
  int get length => _items.length;
}

class ArchivedTaskListBad extends TaskListBad {
  @override
  void add(String title) =>
      throw UnsupportedError('Arşivlenmiş listeye görev eklenemez');
}

// Düzeltme: hiyerarşiyi bölerek "ekleyebilme" yeteneğini ayrı bir tipe taşı.
// Böylece "eklenebilir liste" isteyen kod, arşiv listesini ALAMAZ BİLE.
// (Dikkat: bu düzeltme aynı zamanda BÖLÜM 2'nin — ISP'nin — ta kendisi.)
abstract interface class ReadableTaskList {
  int get length;
  String itemAt(int index);
}

abstract interface class MutableTaskList implements ReadableTaskList {
  void add(String title);
}

class ActiveTaskList implements MutableTaskList {
  final List<String> _items = [];

  @override
  void add(String title) => _items.add(title);

  @override
  int get length => _items.length;

  @override
  String itemAt(int index) => _items[index];
}

class ArchivedTaskList implements ReadableTaskList {
  final List<String> _items;
  ArchivedTaskList(List<String> items) : _items = List.unmodifiable(items);

  @override
  int get length => _items.length;

  @override
  String itemAt(int index) => _items[index];
}

/// Bu fonksiyon derleyici seviyesinde korunuyor: arşiv listesi buraya
/// parametre olarak GEÇEMEZ. Çalışma zamanı sürprizi yok.
void addTodaysHomework(MutableTaskList list, String title) => list.add(title);

void bolum1LspCheck() {
  print('=== BÖLÜM 1 — LSP CHECK ===');

  final buyukAvatar = List.filled(5000, 0); // 5 KB

  print('- Normal depo (üst tip):');
  final normal = ImageStore();
  uploadAvatar(normal, 'u1', buyukAvatar);
  print('  yüklendi, kayıt sayısı: ${normal.count}');

  print('- Aynı çağrı, alt tip ile:');
  try {
    uploadAvatar(ThumbnailStoreBad(), 'u1', buyukAvatar);
    print('  yüklendi');
  } catch (e) {
    print('  PATLADI -> $e');
    print(
      '  Çağıran kod hiç değişmedi; sadece nesne değişti. İşte LSP ihlali.',
    );
  }

  print('- Düzeltilmiş tasarım (sözleşme herkes için aynı):');
  for (final store in <SizedImageStore>[DiskImageStore(), ThumbnailStore()]) {
    final ok = uploadAvatarSafely(store, 'u1', buyukAvatar);
    print('  ${store.runtimeType}: kabul edildi mi = $ok  (sürpriz yok)');
  }

  print('- Mirası reddetme:');
  try {
    ArchivedTaskListBad().add('yeni görev');
  } catch (e) {
    print('  PATLADI -> $e');
  }
  final aktif = ActiveTaskList();
  addTodaysHomework(aktif, 'Matematik s.42');
  print('  Yeni tasarım: aktif listeye eklendi, uzunluk = ${aktif.length}');
  print('  ArchivedTaskList bu fonksiyona derleme hatası olmadan geçemez.\n');
}

// ============================================================================
// BÖLÜM 2 — ISP SPLIT (Interface Segregation Principle)
//
// Kural: Hiçbir sınıf, KULLANMADIĞI metotları uygulamak zorunda kalmasın.
// Şişman interface'in bedeli iki yerde çıkar:
//   1) Uygulayan sınıflar boş/UnimplementedError gövdeler yazar.
//   2) Bağımlı olan sınıflar, ihtiyaç duymadıkları şeylere de bağlanır;
//      o metotlar değişince gereksiz yere etkilenirler.
// ============================================================================

// ------------------------------------------------------------ 2A: ŞİŞMAN HÂL
abstract interface class AuthServiceFat {
  Future<String> signIn(String email, String password);
  Future<void> signOut();
  Future<void> sendPasswordReset(String email);
  Future<void> deleteAccount(String uid);
  Future<List<String>> listAllUsers(); // sadece öğretmen/admin paneli kullanır
  Future<void> assignRole(String uid, String role); // sadece admin
}

/// Giriş ekranının aslında TEK bir metoda ihtiyacı var: signIn.
/// Ama şişman interface yüzünden 6 metotluk bir yüzeye bağımlı hâle geldi.
class LoginControllerBad {
  final AuthServiceFat _auth;
  LoginControllerBad(this._auth);

  Future<String> login(String email, String password) =>
      _auth.signIn(email, password);
}

/// Test yazmak için sahte nesne: 6 metodun 5'i tamamen çöp.
class FakeAuthFat implements AuthServiceFat {
  @override
  Future<String> signIn(String email, String password) async => 'uid-fake';

  @override
  Future<void> signOut() async => throw UnimplementedError();

  @override
  Future<void> sendPasswordReset(String email) async =>
      throw UnimplementedError();

  @override
  Future<void> deleteAccount(String uid) async => throw UnimplementedError();

  @override
  Future<List<String>> listAllUsers() async => throw UnimplementedError();

  @override
  Future<void> assignRole(String uid, String role) async =>
      throw UnimplementedError();
}

// ------------------------------------------------- 2B: BÖLÜNMÜŞ SÖZLEŞMELER
// Bölme ölçütü "metot sayısı" değil, MÜŞTERİ (client) grubudur:
// giriş ekranı, oturum yöneticisi, şifre ekranı, admin paneli.
abstract interface class SignInGateway {
  Future<String> signIn(String email, String password);
}

abstract interface class SessionGateway {
  Future<void> signOut();
}

abstract interface class PasswordRecoveryGateway {
  Future<void> sendPasswordReset(String email);
}

abstract interface class UserDirectory {
  Future<List<String>> listAllUsers();
  Future<void> assignRole(String uid, String role);
}

/// Somut sınıf isterse birkaç sözleşmeyi birden uygulayabilir.
/// ISP, UYGULAYANI değil, ÇAĞIRANI korur.
class FirebaseAuthAdapter
    implements SignInGateway, SessionGateway, PasswordRecoveryGateway {
  @override
  Future<String> signIn(String email, String password) async {
    print('  [Firebase] signIn($email)');
    return 'uid-123';
  }

  @override
  Future<void> signOut() async => print('  [Firebase] signOut()');

  @override
  Future<void> sendPasswordReset(String email) async =>
      print('  [Firebase] sendPasswordReset($email)');
}

class FirebaseAdminAdapter implements UserDirectory {
  @override
  Future<List<String>> listAllUsers() async => ['uid-1', 'uid-2'];

  @override
  Future<void> assignRole(String uid, String role) async =>
      print('  [Firebase] assignRole($uid, $role)');
}

/// Artık sadece ihtiyacı olan tek sözleşmeye bağlı.
class LoginController {
  final SignInGateway _signIn;
  LoginController(this._signIn);

  Future<String> login(String email, String password) =>
      _signIn.signIn(email, password);
}

/// Sahte nesne tek satır. Testi yazmak da okumak da ucuzladı.
class FakeSignIn implements SignInGateway {
  @override
  Future<String> signIn(String email, String password) async => 'uid-fake';
}

Future<void> bolum2IspSplit() async {
  print('=== BÖLÜM 2 — ISP SPLIT ===');

  print('- Şişman interface ile:');
  final bad = LoginControllerBad(FakeAuthFat());
  print('  login -> ${await bad.login('a@b.com', '123456')}');
  print('  (bu testi kurmak için 6 metot yazmak zorunda kaldık)');

  print('- Bölünmüş sözleşmelerle:');
  final good = LoginController(FirebaseAuthAdapter());
  print('  login -> ${await good.login('a@b.com', '123456')}');
  final test = LoginController(FakeSignIn());
  print('  sahte ile login -> ${await test.login('a@b.com', 'x')}');
  print(
    '  (sahte nesne tek metot; admin metotları LoginController\'ı ilgilendirmiyor)\n',
  );
}

// ============================================================================
// BÖLÜM 3 — DIP WIRE (Dependency Inversion Principle)
//
// Kural: Yüksek seviye modül (iş kuralı) düşük seviye modüle (FCM, SQL, HTTP)
// bağlı olmasın; İKİSİ DE bir soyutlamaya bağlı olsun.
//
// Kritik ayrıntı: soyutlamayı DÜŞÜK seviye değil, YÜKSEK seviye tanımlar ve
// kendi diliyle konuşur. 'notifyParent(...)' iş dili; 'sendPush(token, ...)'
// altyapı dili. Bağımlılık okunun yönü böyle tersine döner ("inversion").
// ============================================================================

// ------------------------------------------------------- Düşük seviye detay
class FcmSender {
  void sendPush(String deviceToken, String body) =>
      print('  [FCM] $deviceToken <- "$body"');
}

class SmsGateway {
  void sendSms(String phone, String text) => print('  [SMS] $phone <- "$text"');
}

// -------------------------------------------------------------- 3A: KÖTÜ HÂL
// İş kuralı sınıfı FCM'i kendisi üretiyor (new). Sonuçları:
//  * Test için gerçek FCM lazım.
//  * SMS'e geçmek istersen iş kuralını değiştirmen gerekir (OCP de ihlal).
//  * 'parentToken' parametresi altyapı detayını iş kuralının imzasına sızdırdı.
class HomeworkPolicyBad {
  final FcmSender _fcm = FcmSender();

  void completeHomework(String studentName, String parentToken) {
    print('  $studentName ödevini tamamladı (kayıt edildi)');
    _fcm.sendPush(parentToken, '$studentName ödevini tamamladı');
  }
}

// -------------------------------------------------------------- 3B: İYİ HÂL
/// Soyutlama, yüksek seviyenin ihtiyacını anlatır: "veliyi haberdar et".
/// Token, telefon, retry, kuyruk... hepsi bunun ARKASINDA kalır.
abstract interface class ParentNotifier {
  void notifyParent(String parentId, String message);
}

class FcmParentNotifier implements ParentNotifier {
  final FcmSender _fcm;
  final Map<String, String> _tokensByParent;

  FcmParentNotifier(this._fcm, this._tokensByParent);

  @override
  void notifyParent(String parentId, String message) {
    final token = _tokensByParent[parentId];
    if (token == null) {
      print('  [FCM] $parentId için token yok, atlandı');
      return;
    }
    _fcm.sendPush(token, message);
  }
}

class SmsParentNotifier implements ParentNotifier {
  final SmsGateway _sms;
  final Map<String, String> _phonesByParent;

  SmsParentNotifier(this._sms, this._phonesByParent);

  @override
  void notifyParent(String parentId, String message) {
    final phone = _phonesByParent[parentId];
    if (phone == null) return;
    _sms.sendSms(phone, message);
  }
}

/// Test için: hiçbir ağ çağrısı yok, sadece kayıt tutuyor.
class RecordingParentNotifier implements ParentNotifier {
  final List<String> sent = [];

  @override
  void notifyParent(String parentId, String message) =>
      sent.add('$parentId|$message');
}

/// Yüksek seviye politika. İçinde tek bir 'new FcmSender()' yok.
/// Bağımlılık dışarıdan enjekte ediliyor (constructor injection).
class HomeworkPolicy {
  final ParentNotifier _notifier;

  HomeworkPolicy(this._notifier);

  void completeHomework(String studentName, String parentId) {
    // İş kuralı burada; kanal detayı burada DEĞİL.
    _notifier.notifyParent(parentId, '$studentName ödevini tamamladı');
  }
}

void bolum3DipWire() {
  print('=== BÖLÜM 3 — DIP WIRE ===');

  print('- Çakılı bağımlılık (kötü):');
  HomeworkPolicyBad().completeHomework('Ayşe', 'fcm-token-abc');

  print('- Enjekte edilmiş bağımlılık (iyi):');
  // "Composition root": nesnelerin bağlandığı tek yer (Flutter'da main.dart).
  final fcm = FcmParentNotifier(FcmSender(), {'p1': 'fcm-token-abc'});
  final sms = SmsParentNotifier(SmsGateway(), {'p1': '+90555...'});

  HomeworkPolicy(fcm).completeHomework('Ayşe', 'p1');
  HomeworkPolicy(sms).completeHomework('Ayşe', 'p1'); // politika değişmedi
  print(
    '  Aynı politika, iki farklı kanal. HomeworkPolicy\'nin tek satırı bile değişmedi.\n',
  );
}

// ============================================================================
// BÖLÜM 4 — VIOLATION HUNT (mini test koşucusu)
//
// Her ilke için: önce ihlalin gerçekten patladığını kanıtla,
// sonra düzeltilmiş tasarımın sakin çalıştığını kanıtla.
// ============================================================================

int _passed = 0;
int _failed = 0;

void check(String name, bool Function() body) {
  bool ok;
  try {
    ok = body();
  } catch (e) {
    ok = false;
    print('   beklenmeyen hata: $e');
  }
  if (ok) {
    _passed++;
    print('  [GEÇTİ] $name');
  } else {
    _failed++;
    print('  [KALDI] $name');
  }
}

Future<void> checkAsync(String name, Future<bool> Function() body) async {
  bool ok;
  try {
    ok = await body();
  } catch (e) {
    ok = false;
    print('   beklenmeyen hata: $e');
  }
  if (ok) {
    _passed++;
    print('  [GEÇTİ] $name');
  } else {
    _failed++;
    print('  [KALDI] $name');
  }
}

bool throws(void Function() body) {
  try {
    body();
    return false;
  } catch (_) {
    return true;
  }
}

Future<void> bolum4ViolationHunt() async {
  print('=== BÖLÜM 4 — VIOLATION HUNT ===');

  final besKb = List.filled(5000, 0);

  // --- LSP ---
  check(
    'LSP ihlali: alt tip, üst tip sözleşmesini bozuyor',
    () => throws(() => uploadAvatar(ThumbnailStoreBad(), 'u1', besKb)),
  );

  check(
    'LSP düzeltmesi: iki depo da aynı sözleşmeye uyuyor, kimse patlamıyor',
    () {
      final disk = uploadAvatarSafely(DiskImageStore(), 'u1', besKb);
      final thumb = uploadAvatarSafely(ThumbnailStore(), 'u1', besKb);
      return disk == true && thumb == false;
    },
  );

  check(
    'LSP ihlali: miras reddi (UnsupportedError)',
    () => throws(() => ArchivedTaskListBad().add('x')),
  );

  check(
    'LSP düzeltmesi: arşiv listesi okunabiliyor, yazma yeteneği hiç yok',
    () => ArchivedTaskList(['eski görev']).length == 1,
  );

  // --- ISP ---
  await checkAsync(
    'ISP düzeltmesi: sahte nesne tek metotla kuruluyor',
    () async {
      final uid = await LoginController(FakeSignIn()).login('a@b.com', 'x');
      return uid == 'uid-fake';
    },
  );

  await checkAsync(
    'ISP ihlali: şişman sahte nesnenin kullanılmayan metotları çöp',
    () async => await _fatFakePatlarMi(),
  );

  // --- DIP ---
  check('DIP düzeltmesi: politika, ağ olmadan test edilebiliyor', () {
    final kayit = RecordingParentNotifier();
    HomeworkPolicy(kayit).completeHomework('Ayşe', 'p1');
    return kayit.sent.length == 1 &&
        kayit.sent.first == 'p1|Ayşe ödevini tamamladı';
  });

  check('DIP düzeltmesi: kanal değişince politika değişmiyor', () {
    final kayit = RecordingParentNotifier();
    final policy = HomeworkPolicy(kayit); // aynı sınıf, farklı bağımlılık
    policy.completeHomework('Mehmet', 'p2');
    return kayit.sent.single.startsWith('p2|');
  });

  print('\n  Sonuç: $_passed geçti, $_failed kaldı.');
}

/// Şişman sahte nesnenin kullanılmayan metotları çağrılınca patlıyor mu?
/// (Future attığı için ayrı bir yardımcı gerekiyor.)
Future<bool> _fatFakePatlarMi() async {
  try {
    await FakeAuthFat().assignRole('u', 'admin');
    return false;
  } catch (_) {
    return true;
  }
}

// ============================================================================
// MAIN
// ============================================================================
Future<void> main() async {
  bolum1LspCheck();
  await bolum2IspSplit();
  bolum3DipWire();
  await bolum4ViolationHunt();
}
