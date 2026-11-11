/// Web Share v2 (bible chapter 11, J7): a phone or a PC without medXfer sends and receives
/// files with its browser. This model drives the core's guest session (web_share_start, add,
/// remove, stop, answer, cancel), follows its web_share_* events, and keeps what the host chose
/// before it: the screen may be left and found again as it was (real test of 10 November 2026).
library;

import 'dart:async';

import 'package:flutter/foundation.dart';

import '../core/core.dart';
import '../library/selection.dart';
import '../platform/file_picker.dart';

/// The guest session running: what the host's screen shows. The token and the PIN reach this
/// screen only (WEB-04).
@immutable
class WebShareSession {
  const WebShareSession({
    required this.url,
    required this.pin,
    required this.fingerprint,
    required this.address,
    required this.receive,
    required this.expires,
    this.kind = '',
  });

  factory WebShareSession.fromMap(Map<String, Object?> m) {
    final addr = '${m['addr'] ?? ''}';
    final port = (m['port'] as num?)?.toInt() ?? 0;
    return WebShareSession(
      url: '${m['url'] ?? ''}',
      pin: '${m['pin'] ?? ''}',
      fingerprint: '${m['fingerprint'] ?? ''}',
      address: port > 0 ? 'https://$addr:$port' : addr,
      receive: m['receive'] == true,
      expires: DateTime.tryParse('${m['expires'] ?? ''}')?.toLocal(),
      kind: '${m['kind'] ?? ''}',
    );
  }

  /// The kind of the network served on: "wifi", "ethernet" or "other". The guest must be on it.
  final String kind;

  /// What the QR code carries: the token in its fragment.
  final String url;

  /// 6 digits, for a guest without a camera.
  final String pin;

  /// The certificate's SHA-256, as browsers show it (decision D6).
  final String fingerprint;

  /// The address a guest types, without the token.
  final String address;

  /// The guest may send files here.
  final bool receive;
  final DateTime? expires;

  /// The PIN as it is read out: "054 461".
  String get pinShown => pin.length == 6 ? '${pin.substring(0, 3)} ${pin.substring(3)}' : pin;
}

/// Something to offer, chosen before the session: from the home's selection (opened for the
/// core at the start only), a path (desktops), or a file open already (Android's picker).
@immutable
class WebShareDraft {
  const WebShareDraft.pick(Pick this.pick) : path = null, file = null;
  const WebShareDraft.path(String this.path) : pick = null, file = null;
  const WebShareDraft.file(OpenFile this.file) : pick = null, path = null;

  final Pick? pick;
  final String? path;
  final OpenFile? file;

  String get name => pick?.name ?? file?.name ?? path!.split(RegExp(r'[\\/]')).last;
  bool get folder => pick?.kind == PickKind.folder;
}

/// Something offered, the session running: its ID in the core, for the host to withdraw it.
@immutable
class WebShareItem {
  const WebShareItem({required this.id, required this.name, required this.size, required this.folder});

  factory WebShareItem.fromMap(Map<Object?, Object?> m) => WebShareItem(
        id: '${m['id'] ?? ''}',
        name: '${m['name'] ?? ''}',
        size: (m['size'] as num?)?.toInt() ?? 0,
        folder: m['folder'] == true,
      );

  final String id;
  final String name;
  final int size;
  final bool folder;
}

/// A file a guest announces.
@immutable
class WebShareFile {
  const WebShareFile(this.name, this.size);

  final String name;
  final int size;
}

/// The guest's upload: waiting for the host's answer, then coming.
class WebShareUpload {
  WebShareUpload({required this.ticket, required this.files, required this.total, this.accepted = false});

  final String ticket;
  final List<WebShareFile> files;
  final int total;
  bool accepted;
  int received = 0;
}

/// A network the session may serve on (web_share_networks): never mobile data. The guest must be
/// on the same one.
@immutable
class WebShareNetwork {
  const WebShareNetwork(this.ip, this.kind);

  factory WebShareNetwork.fromMap(Map<Object?, Object?> m) => WebShareNetwork('${m['ip'] ?? ''}', '${m['kind'] ?? ''}');

  final String ip;

  /// "wifi", "ethernet" or "other".
  final String kind;
}

/// Why the session ended, as web_share_closed says it.
enum WebShareEnd { stopped, expired, pinAttempts }

/// What picks become for the core: paths, or files open by the app.
typedef OpenPicks = Future<({List<String> paths, List<OpenFile> files})> Function(List<Pick> picks);

class WebShare extends ChangeNotifier {
  /// [openPicks]: the home's picks made ready for the core (AppState: paths on a desktop, files
  /// open by the app on Android). [closeFiles]: files picked that will not reach the core.
  WebShare(this.core, {OpenPicks? openPicks, Future<void> Function(List<OpenFile> files)? closeFiles})
      : _openPicks = openPicks ?? _byPath,
        _closeFiles = closeFiles ?? _closeNothing {
    _sub = core.events.listen(take);
  }

  static Future<({List<String> paths, List<OpenFile> files})> _byPath(List<Pick> picks) async =>
      (paths: [for (final p in picks) p.path], files: const <OpenFile>[]);
  static Future<void> _closeNothing(List<OpenFile> files) async {}

  final Core core;
  final OpenPicks _openPicks;
  final Future<void> Function(List<OpenFile> files) _closeFiles;
  late final StreamSubscription<CoreEvent> _sub;

  WebShareSession? _session;
  bool _starting = false;
  String? _error;
  WebShareEnd? _ended;
  WebShareUpload? _upload;
  final _received = <String>[];
  final _items = <WebShareItem>[];
  int _downloads = 0;
  final _draft = <WebShareDraft>[];
  bool _receive = true;
  bool _autoAccept = false;
  bool _wanted = false;
  List<WebShareNetwork> _networks = const [];
  String? _network;

  /// The session running, or null.
  WebShareSession? get session => _session;
  bool get starting => _starting;

  /// Why the last start, addition or withdrawal failed (the core's words).
  String? get error => _error;

  /// Why the last session ended; null while one runs, or before any.
  WebShareEnd? get ended => _ended;

  /// The guest's upload: waiting for an answer (not [WebShareUpload.accepted]), or coming.
  WebShareUpload? get upload => _upload;

  /// What the guest is offered, the session running, in order.
  List<WebShareItem> get items => List.unmodifiable(_items);

  /// The names of the files received in this session, newest last.
  List<String> get received => List.unmodifiable(_received);

  /// The downloads the guest finished (Chrome on Android starts each one several times, real
  /// test of 10 November 2026: only those finished are counted).
  int get downloads => _downloads;

  /// Something runs that the device must stay awake for (#256).
  bool get active => _session != null;

  // Before the session: kept here, not on the screen, which may be left and found again.

  /// What will be offered at the start.
  List<WebShareDraft> get draft => List.unmodifiable(_draft);

  /// The guest may send files into the receive folder.
  bool get receive => _receive;
  set receive(bool on) {
    _receive = on;
    notifyListeners();
  }

  /// The guest's uploads accepted without asking (WEB-06).
  bool get autoAccept => _receive && _autoAccept;
  set autoAccept(bool on) {
    _autoAccept = on;
    notifyListeners();
  }

  /// The networks a session may serve on, best first (Wi-Fi first, never mobile data), as last
  /// read by [loadNetworks]. Several: the host chooses the guest's ([network]).
  List<WebShareNetwork> get networks => List.unmodifiable(_networks);

  /// The address of the network chosen; the first of [networks] when none was, or when the one
  /// chosen is gone.
  String? get network => _networks.any((n) => n.ip == _network) ? _network : (_networks.isEmpty ? null : _networks.first.ip);
  set network(String? ip) {
    _network = ip;
    notifyListeners();
  }

  /// Reads the networks again (the screen opens, a network came or went). Real tests of 10
  /// November 2026: the core served on the mobile data, or on a cable where the guest was not.
  Future<void> loadNetworks() async {
    final reply = await core.call('web_share_networks');
    final list = reply.first('web_share_networks')?.map['networks'];
    _networks = [
      for (final m in (list as List?) ?? const [])
        if (m is Map) WebShareNetwork.fromMap(m),
    ];
    notifyListeners();
  }

  /// The same file or folder twice is listed once, from the home's selection as from a picker.
  void addPicks(Iterable<Pick> picks) => _addDraft([for (final p in {for (final p in picks) p.path: p}.values) WebShareDraft.pick(p)]);
  void addPaths(Iterable<String> paths) => _addDraft([for (final p in paths.toSet()) WebShareDraft.path(p)]);
  void addFiles(Iterable<OpenFile> files) => _addDraft([for (final f in files) WebShareDraft.file(f)]);

  void _addDraft(List<WebShareDraft> list) {
    final more = [
      for (final d in list)
        if (!_draft.any((o) => (d.path != null && o.path == d.path) || (d.pick != null && o.pick?.path == d.pick!.path))) d,
    ];
    if (more.isEmpty) return;
    _draft.addAll(more);
    notifyListeners();
  }

  /// Leaves one aside; a file open already is closed.
  Future<void> removeDraft(int index) async {
    if (index < 0 || index >= _draft.length) return;
    final d = _draft.removeAt(index);
    notifyListeners();
    if (d.file != null) await _closeFiles([d.file!]);
  }

  /// The screen is asked for (the desktop's side menu shows its page): [wanted] until [shown].
  bool get wanted => _wanted;
  void show() {
    _wanted = true;
    notifyListeners();
  }

  void shown() => _wanted = false;

  /// The home's selection to a browser: added to the session running, else to what will be
  /// offered, the screen asked for. False when the core refused ([error] says why).
  Future<bool> offerSelection(Iterable<Pick> picks) async {
    final list = picks.toList();
    if (list.isEmpty) return false;
    if (_session != null) return add(picks: list);
    addPicks(list);
    show();
    return true;
  }

  /// Starts the guest session with what was chosen ([draft], [receive], [autoAccept]).
  /// [address]: the interface to serve on (this device's network made for the occasion when it
  /// runs), else the [network] chosen, else the core's own choice (its first).
  Future<void> start({String? address}) async {
    address ??= network;
    if (_starting || _session != null) return;
    _starting = true;
    _error = null;
    _ended = null;
    _upload = null;
    _received.clear();
    _items.clear();
    _downloads = 0;
    notifyListeners();
    try {
      final fromHome = await _openPicks([for (final d in _draft) if (d.pick != null) d.pick!]);
      final paths = [for (final d in _draft) if (d.path != null) d.path!, ...fromHome.paths];
      final files = [for (final d in _draft) if (d.file != null) d.file!, ...fromHome.files];
      // The files open are the core's from the call on, whatever it answers.
      _draft.removeWhere((d) => d.file != null);
      final reply = await core.call('web_share_start', {
        if (paths.isNotEmpty) 'paths': paths,
        if (files.isNotEmpty) 'items': [for (final f in files) f.toJson()],
        'receive': _receive,
        'auto_accept': autoAccept,
        if (address != null && address.isNotEmpty) 'addr': address,
      });
      final started = reply.first('web_share_started');
      if (started != null) {
        _session = WebShareSession.fromMap(started.map);
        _items.addAll(_itemsOf(started.map['items']));
        _draft.clear();
      } else {
        _error = reply.first('action_error')?.map['error'] as String? ?? reply.error ?? 'web_share_start';
      }
    } finally {
      _starting = false;
      notifyListeners();
    }
  }

  static List<WebShareItem> _itemsOf(Object? list) => [
        for (final m in (list as List?) ?? const [])
          if (m is Map) WebShareItem.fromMap(m),
      ];

  /// Offers more, the session running (real test of 10 November 2026): the guest's page lists
  /// them within seconds. False when the core refused ([error] says why).
  Future<bool> add({List<String> paths = const [], List<OpenFile> files = const [], List<Pick> picks = const []}) async {
    if (_session == null) {
      await _closeFiles(files);
      return false;
    }
    final fromHome = await _openPicks(picks);
    final allPaths = [...paths, ...fromHome.paths];
    final allFiles = [...files, ...fromHome.files];
    if (allPaths.isEmpty && allFiles.isEmpty) return false;
    final reply = await core.call('web_share_add', {
      if (allPaths.isNotEmpty) 'paths': allPaths,
      if (allFiles.isNotEmpty) 'items': [for (final f in allFiles) f.toJson()],
    });
    final added = reply.first('web_share_added');
    if (added == null) {
      _error = reply.first('action_error')?.map['error'] as String? ?? reply.error ?? 'web_share_add';
      notifyListeners();
      return false;
    }
    _error = null;
    _items.addAll(_itemsOf(added.map['items']));
    notifyListeners();
    return true;
  }

  /// Withdraws an item from the guest's list, the session running.
  Future<void> remove(String id) async {
    if (_session == null) return;
    final reply = await core.call('web_share_remove', {'id': id});
    if (reply.first('web_share_removed') != null) {
      _items.removeWhere((it) => it.id == id);
      _error = null;
    } else {
      _error = reply.first('action_error')?.map['error'] as String? ?? reply.error ?? 'web_share_remove';
    }
    notifyListeners();
  }

  /// Ends the session: the listener, the certificate, the token and the PIN go (WEB-10).
  Future<void> stop() async {
    if (_session == null) return;
    await core.call('web_share_stop');
  }

  /// The host's answer to the guest's upload (WEB-06).
  Future<void> answer(bool accept) async {
    final up = _upload;
    if (up == null || up.accepted) return;
    if (!accept) {
      _upload = null;
      notifyListeners();
    }
    await core.call('web_share_answer', {'ticket': up.ticket, 'accept': accept});
  }

  /// The host stops the upload coming: the incomplete files are removed.
  Future<void> cancelUpload() async {
    final up = _upload;
    if (up == null) return;
    await core.call('web_share_cancel', {'ticket': up.ticket});
  }

  @visibleForTesting
  void take(CoreEvent e) {
    if (!e.name.startsWith('web_share_')) return;
    final m = e.map;
    final ticket = '${m['ticket'] ?? ''}';
    final mine = _upload?.ticket == ticket;
    switch (e.name) {
      case 'web_share_closed':
        if (_session == null) return;
        _session = null;
        _upload = null;
        _items.clear();
        _ended = switch (m['reason']) {
          'expired' => WebShareEnd.expired,
          'pin_attempts' => WebShareEnd.pinAttempts,
          _ => WebShareEnd.stopped,
        };
      case 'web_share_upload_request':
        _upload = WebShareUpload(
          ticket: ticket,
          total: (m['total'] as num?)?.toInt() ?? 0,
          accepted: m['auto'] == true,
          files: [
            for (final f in (m['files'] as List?) ?? const [])
              if (f is Map) WebShareFile('${f['name'] ?? ''}', (f['size'] as num?)?.toInt() ?? 0),
          ],
        );
      case 'web_share_upload_accepted' when mine:
        _upload!.accepted = true;
      case 'web_share_upload_progress' when mine:
        _upload!.received = (m['received'] as num?)?.toInt() ?? _upload!.received;
      case 'web_share_upload_file_done':
        _received.add('${m['name'] ?? ''}');
      case 'web_share_upload_done' || 'web_share_upload_refused' || 'web_share_upload_cancelled' when mine:
        _upload = null;
      case 'web_share_download_done':
        _downloads++;
      default:
        return;
    }
    notifyListeners();
  }

  @override
  void dispose() {
    unawaited(_sub.cancel());
    super.dispose();
  }
}
