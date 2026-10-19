/// The state the screens share, over the core: is it started, what is this device called.
/// Plain ChangeNotifier (decision J4: no state library).
library;

import 'dart:async';

import 'package:flutter/foundation.dart';

import 'connection/connection.dart';
import 'core/core.dart';
import 'platform/folders.dart';
import 'platform/network.dart';

enum CoreStatus { starting, ready, failed }

class AppState extends ChangeNotifier {
  AppState({required this.core, required this.folders, NetworkInfo network = const NoNetworkInfo()})
      : connection = Connection(core, network: network);

  final Core core;
  final Folders folders;

  /// The other device: pairing, session, trust (WP-B.2). Listens to the core from the start.
  final Connection connection;

  CoreStatus _status = CoreStatus.starting;
  String _deviceName = '';
  String? _error;
  StreamSubscription<CoreEvent>? _events;

  CoreStatus get status => _status;

  /// This device's name, as the core knows it (sent to a device once paired, DISC-06).
  String get deviceName => _deviceName;
  String? get error => _error;

  /// Starts the core and asks for its status.
  Future<void> start() async {
    _events ??= core.events.listen(_onEvent);
    try {
      final started = await core.start(CoreConfig(dataDir: await folders.dataDir(), downloadDir: await folders.downloadDir()));
      if (!started.ok) {
        return _fail(started.error ?? 'unknown error');
      }
      if (started.schema != coreSchema) {
        return _fail('schema ${started.schema}, this interface speaks $coreSchema');
      }
      await connection.pushNetwork(); // before the status: its address comes from it (DISC-10)
      final reply = await core.call('get_status');
      final status = reply.first('status');
      if (status != null) _takeStatus(status);
      _status = CoreStatus.ready;
      notifyListeners();
    } catch (e) {
      _fail('$e');
    }
  }

  void _fail(String error) {
    _status = CoreStatus.failed;
    _error = error;
    notifyListeners();
  }

  void _onEvent(CoreEvent e) {
    if (e.name == 'status') _takeStatus(e);
  }

  void _takeStatus(CoreEvent e) {
    final name = e.map['device_name'];
    if (name is String && name != _deviceName) {
      _deviceName = name;
      notifyListeners();
    }
  }

  @override
  void dispose() {
    _events?.cancel();
    connection.dispose();
    super.dispose();
  }
}
