/// Web Share v2 (bible chapter 11, J7): a phone or a PC without medXfer sends and receives
/// files with its browser. This model drives the core's guest session (web_share_start, stop,
/// answer, cancel) and follows its web_share_* events for the host's screen.
library;

import 'dart:async';

import 'package:flutter/foundation.dart';

import '../core/core.dart';
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
    );
  }

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

/// Why the session ended, as web_share_closed says it.
enum WebShareEnd { stopped, expired, pinAttempts }

class WebShare extends ChangeNotifier {
  WebShare(this.core) {
    _sub = core.events.listen(take);
  }

  final Core core;
  late final StreamSubscription<CoreEvent> _sub;

  WebShareSession? _session;
  bool _starting = false;
  String? _error;
  WebShareEnd? _ended;
  WebShareUpload? _upload;
  final _received = <String>[];
  int _downloads = 0;

  /// The session running, or null.
  WebShareSession? get session => _session;
  bool get starting => _starting;

  /// Why the last start failed (the core's words).
  String? get error => _error;

  /// Why the last session ended; null while one runs, or before any.
  WebShareEnd? get ended => _ended;

  /// The guest's upload: waiting for an answer (not [WebShareUpload.accepted]), or coming.
  WebShareUpload? get upload => _upload;

  /// The names of the files received in this session, newest last.
  List<String> get received => List.unmodifiable(_received);

  /// The downloads the guest finished (Chrome on Android starts each one several times, real
  /// test of 10 November 2026: only those finished are counted).
  int get downloads => _downloads;

  /// Something runs that the device must stay awake for (#256).
  bool get active => _session != null;

  /// Starts the guest session: paths (desktops) or files open already (Android); the guest may
  /// send into the receive folder when [receive]. [address]: the interface to serve on, the
  /// core's own choice when null (this device's network made for the occasion when it runs).
  Future<void> start({List<String> paths = const [], List<OpenFile> files = const [], bool receive = true, bool autoAccept = false, String? address}) async {
    if (_starting) return;
    _starting = true;
    _error = null;
    _ended = null;
    _upload = null;
    _received.clear();
    _downloads = 0;
    notifyListeners();
    try {
      final reply = await core.call('web_share_start', {
        if (paths.isNotEmpty) 'paths': paths,
        if (files.isNotEmpty) 'items': [for (final f in files) f.toJson()],
        'receive': receive,
        'auto_accept': autoAccept,
        if (address != null && address.isNotEmpty) 'addr': address,
      });
      final started = reply.first('web_share_started');
      if (started != null) {
        _session = WebShareSession.fromMap(started.map);
      } else {
        _error = reply.first('action_error')?.map['error'] as String? ?? reply.error ?? 'web_share_start';
      }
    } finally {
      _starting = false;
      notifyListeners();
    }
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
