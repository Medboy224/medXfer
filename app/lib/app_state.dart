/// The state the screens share, over the core: is it started, what is this device called, and
/// the models of the connection (WP-B.2) and of the transfers (WP-B.3).
/// Plain ChangeNotifier (decision J4: no state library).
library;

import 'dart:async';

import 'package:flutter/foundation.dart';

import 'connection/connection.dart';
import 'background_keeper.dart';
import 'dev/dev_tools.dart';
import 'library/explorer.dart';
import 'library/selection.dart';
import 'platform/background.dart';
import 'core/core.dart';
import 'platform/file_picker.dart';
import 'platform/folders.dart';
import 'platform/library.dart';
import 'platform/hotspot.dart';
import 'platform/network.dart';
import 'platform/open_folder.dart';
import 'platform/qr_scanner.dart';
import 'platform/storage.dart';
import 'transfer/history.dart';
import 'transfer/transfers.dart';

enum CoreStatus { starting, ready, failed }

class AppState extends ChangeNotifier {
  AppState({
    required this.core,
    required this.folders,
    NetworkInfo network = const NoNetworkInfo(),
    Hotspot hotspot = const NoHotspot(),
    this.qrScanner = const NoQrScanner(),
    this.filePicker = const NoFilePicker(),
    this.folderOpener = const NoFolderOpener(),
    this.storage = const NoReceiveStorage(),
    Background background = const NoBackground(),
    this.library = const NoLibrary(),
    HistoryStore? history,
  }) : connection = Connection(core, network: network, hotspot: hotspot) {
    // Created now, not on first use: they listen to the core from its first event.
    transfers = Transfers(
      core,
      history: history ?? FileHistoryStore(folders.dataDir),
      peerName: () => connection.session?.name ?? '',
      downloadDir: folders.downloadDir,
      // Android: each offer in medXfer/<type>/ (decision J2), whole.
      placeFor: storage.supported
          ? (o) async => '${await storage.root()}/${typeFolderFor(o.items.isEmpty ? [o.name] : [for (final i in o.items) i.path])}'
          : null,
    );
    explorer = Explorer(library);
    keeper = BackgroundKeeper(background: background, connection: connection, transfers: transfers);
    dev = DevTools(core, downloadDir: folders.downloadDir);
  }

  /// The developer panel's telemetry, benchmarks, report and bundle (WP-B.5).
  late final DevTools dev;

  /// What the phone holds, the explorer's lists, and what the user picked (WP-B.9).
  final Library library;
  late final Explorer explorer;
  final selection = Selection();

  /// The foreground service while something runs (API-08).
  late final BackgroundKeeper keeper;

  final Core core;
  final Folders folders;

  /// The other device: pairing, session, trust (WP-B.2). Listens to the core from the start.
  final Connection connection;

  /// The camera that reads QR codes (decision J5); none on the desktops.
  final QrScanner qrScanner;

  /// Sending and receiving, and their history (WP-B.3).
  late final Transfers transfers;

  /// Files and folders chosen on a desktop; the received folder opened.
  final FilePicker filePicker;
  final FolderOpener folderOpener;

  /// Where received files go on Android, and the access to all files (WP-B.4).
  final ReceiveStorage storage;

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
      if (status != null) {
        _takeStatus(status);
        connection.takeStatus(status); // this device's generic name (DISC-07)
      }
      unawaited(transfers.loadHistory()); // the screens need not wait for it
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
    keeper.dispose();
    explorer.dispose();
    selection.dispose();
    transfers.dispose();
    dev.dispose();
    super.dispose();
  }
}
