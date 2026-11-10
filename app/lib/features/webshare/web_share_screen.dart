/// « Partager avec un navigateur » (WP-W.5 of the J7 plan, bible chapter 11): before the
/// session, what the guest is offered and whether it may send; during it, the QR code, the PIN,
/// the browser's warning explained, the guest's uploads to accept, and what came. Leaving the
/// screen ends the session (WEB-10).
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app.dart';
import '../../app_state.dart';
import '../../l10n/app_localizations.dart';
import '../../library/selection.dart';
import '../../platform/file_picker.dart';
import '../../theme.dart';
import '../../transfer/web_share.dart';
import '../../widgets/format.dart';
import '../../widgets/qr_view.dart';

/// What picks become for the core: paths on a desktop, and for folders on Android; files open by
/// the app otherwise (Android, STO-16), as a send does.
Future<({List<String> paths, List<OpenFile> files})> _offerOf(AppState state, List<Pick> picks) async {
  if (state.desktop) return (paths: [for (final p in picks) p.path], files: const <OpenFile>[]);
  final others = [
    for (final p in picks)
      if (p.kind != PickKind.folder) (path: p.path, name: p.name),
  ];
  return (
    paths: [
      for (final p in picks)
        if (p.kind == PickKind.folder) p.path,
    ],
    files: others.isEmpty ? const <OpenFile>[] : await state.library.open(others),
  );
}

/// The home's selection to a browser (its « Navigateur » button, real test of 10 November 2026):
/// added to the session running, said in a line; else waiting for the screen, which [open]
/// shows. A device with medXfer stays the faster way: « Envoyer » is still beside it.
Future<void> offerSelectionToBrowser(BuildContext context, {required VoidCallback open}) async {
  final state = AppScope.of(context, listen: false).state;
  final l = AppLocalizations.of(context);
  final messenger = ScaffoldMessenger.of(context);
  final picks = state.selection.items.toList();
  if (picks.isEmpty) return;
  state.selection.clear();
  final ws = state.webShare;
  if (ws.session == null) {
    ws.offer(picks);
    open();
    return;
  }
  final offer = await _offerOf(state, picks);
  final ok = await ws.add(paths: offer.paths, files: offer.files, names: [for (final p in picks) p.name]);
  messenger
    ..clearSnackBars()
    ..showSnackBar(SnackBar(content: Text(ok ? l.webShareAdded(picks.length) : l.webShareFailed(ws.error ?? ''))));
}

class WebShareScreen extends StatefulWidget {
  /// [page]: a page of the desktop's side menu, without a back button.
  const WebShareScreen({super.key, this.page = false});

  final bool page;

  @override
  State<WebShareScreen> createState() => _WebShareScreenState();
}

class _WebShareScreenState extends State<WebShareScreen> {
  late final WebShare _ws = AppScope.of(context, listen: false).state.webShare;
  final _paths = <String>[];
  final _files = <OpenFile>[];
  final _picks = <Pick>[]; // the home's selection, opened at the start only
  bool _receive = true;
  bool _autoAccept = false;

  @override
  void initState() {
    super.initState();
    _ws.addListener(_selectionCame);
    WidgetsBinding.instance.addPostFrameCallback((_) => _selectionCame());
  }

  /// The home's selection sent here, the session not started: listed with the rest.
  void _selectionCame() {
    if (!mounted || !_ws.wanted || _ws.session != null) return;
    setState(() => _picks.addAll(_ws.takePending()));
  }

  @override
  void dispose() {
    _ws.removeListener(_selectionCame);
    // Leaving the screen ends the session (WEB-10). Not during the unmount: the model tells its
    // listeners, and the tree is locked now.
    final ws = _ws;
    scheduleMicrotask(() => unawaited(ws.stop()));
    super.dispose();
  }

  List<String> get _names => [for (final p in _picks) p.name, for (final p in _paths) _base(p), for (final f in _files) f.name];

  static String _base(String path) => path.split(RegExp(r'[\\/]')).last;

  Future<void> _pickFiles() async {
    final picked = await AppScope.of(context, listen: false).state.filePicker.files();
    if (!mounted || picked.isEmpty) return;
    setState(() {
      _paths.addAll(picked.paths);
      _files.addAll(picked.files);
    });
  }

  Future<void> _pickFolder() async {
    final folder = await AppScope.of(context, listen: false).state.filePicker.folder();
    if (!mounted || folder == null) return;
    setState(() => _paths.add(folder));
  }

  Future<void> _start() async {
    final state = AppScope.of(context, listen: false).state;
    final names = _names;
    final fromHome = await _offerOf(state, List.of(_picks));
    _picks.clear();
    // The files open are the core's from now on: handed once.
    final files = [..._files, ...fromHome.files];
    _files.clear();
    await _ws.start(
      paths: [..._paths, ...fromHome.paths],
      files: files,
      names: names,
      receive: _receive,
      autoAccept: _receive && _autoAccept,
      address: state.connection.myHotspot?.address,
    );
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final state = AppScope.of(context).state;
    return Scaffold(
      appBar: AppBar(title: Text(l.webShareTitle), automaticallyImplyLeading: !widget.page),
      body: ListenableBuilder(
        listenable: Listenable.merge([_ws, state.connection]),
        builder: (context, _) => Align(
          alignment: Alignment.topCenter,
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 560),
            child: ListView(
              padding: const EdgeInsets.all(16),
              children: _ws.session == null ? _setup(context) : _running(context, _ws.session!),
            ),
          ),
        ),
      ),
    );
  }

  /// Before the session: what is offered, whether the guest may send, the network.
  List<Widget> _setup(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final state = AppScope.of(context).state;
    final picker = state.filePicker;
    final connection = state.connection;
    final names = _names;
    final hotspot = connection.myHotspot;
    return [
      Text(l.webShareIntro, style: TextStyle(color: c.muted, height: 1.4)),
      if (_ws.ended case final ended? when ended != WebShareEnd.stopped) ...[
        const SizedBox(height: 12),
        Text(
          key: const Key('web-share-ended'),
          switch (ended) {
            WebShareEnd.expired => l.webShareEndedExpired,
            WebShareEnd.pinAttempts => l.webShareEndedPin,
            WebShareEnd.stopped => '', // asked by the host: nothing to say
          },
          style: TextStyle(color: c.danger),
        ),
      ],
      const SizedBox(height: 20),
      Text(l.webShareOffered, style: const TextStyle(fontWeight: FontWeight.w700)),
      const SizedBox(height: 6),
      if (names.isEmpty)
        Text(l.webShareNothingOffered, style: TextStyle(color: c.muted))
      else
        for (final n in names)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 2),
            child: Row(children: [Icon(Icons.insert_drive_file_outlined, size: 18, color: c.muted), const SizedBox(width: 8), Expanded(child: Text(n))]),
          ),
      const SizedBox(height: 8),
      Wrap(
        spacing: 8,
        runSpacing: 8,
        children: [
          if (picker.supported) OutlinedButton.icon(key: const Key('web-share-files'), onPressed: _pickFiles, icon: const Icon(Icons.add_rounded), label: Text(l.webShareChooseFiles)),
          if (picker.folders) OutlinedButton.icon(key: const Key('web-share-folder'), onPressed: _pickFolder, icon: const Icon(Icons.folder_outlined), label: Text(l.webShareChooseFolder)),
        ],
      ),
      const SizedBox(height: 12),
      SwitchListTile(
        key: const Key('web-share-receive'),
        contentPadding: EdgeInsets.zero,
        value: _receive,
        onChanged: (v) => setState(() => _receive = v),
        title: Text(l.webShareReceive),
        subtitle: Text(l.webShareReceiveHint),
      ),
      SwitchListTile(
        key: const Key('web-share-auto'),
        contentPadding: EdgeInsets.zero,
        value: _receive && _autoAccept,
        onChanged: _receive ? (v) => setState(() => _autoAccept = v) : null,
        title: Text(l.webShareAutoAccept),
      ),
      // No common Wi-Fi: this device's own network, which the guest joins first (DISC-12).
      if (hotspot != null)
        Padding(
          padding: const EdgeInsets.symmetric(vertical: 8),
          child: Text(l.webShareNetwork(hotspot.ssid ?? '', hotspot.passphrase ?? ''), key: const Key('web-share-network')),
        )
      else if (connection.hotspot.supported)
        Align(
          alignment: Alignment.centerLeft,
          child: TextButton.icon(
            key: const Key('web-share-make-network'),
            onPressed: connection.hotspotBusy ? null : () => unawaited(connection.startHotspot()),
            icon: const Icon(Icons.wifi_tethering_rounded),
            label: Text(l.webShareMakeNetwork),
          ),
        ),
      if (_ws.error case final error?) ...[
        const SizedBox(height: 8),
        Text(l.webShareFailed(error), style: TextStyle(color: c.danger)),
      ],
      const SizedBox(height: 16),
      FilledButton.icon(
        key: const Key('web-share-start'),
        style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(52)),
        onPressed: _ws.starting || connection.hotspotBusy || (names.isEmpty && !_receive) ? null : () => unawaited(_start()),
        icon: _ws.starting ? const SizedBox.square(dimension: 18, child: CircularProgressIndicator(strokeWidth: 2)) : const Icon(Icons.public_rounded),
        label: Text(l.webShareStart),
      ),
    ];
  }

  /// More offered, the session running: the guest's page lists them within seconds.
  Future<void> _addFiles() async {
    final picked = await AppScope.of(context, listen: false).state.filePicker.files();
    if (picked.isEmpty) return;
    await _ws.add(paths: picked.paths, files: picked.files, names: [for (final p in picked.paths) _base(p), for (final f in picked.files) f.name]);
  }

  Future<void> _addFolder() async {
    final folder = await AppScope.of(context, listen: false).state.filePicker.folder();
    if (folder == null) return;
    await _ws.add(paths: [folder], names: [_base(folder)]);
  }

  /// During the session: how the guest comes in, what it asks, what came.
  List<Widget> _running(BuildContext context, WebShareSession s) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final hotspot = AppScope.of(context).state.connection.myHotspot;
    final picker = AppScope.of(context).state.filePicker;
    final up = _ws.upload;
    final time = s.expires == null ? null : TimeOfDay.fromDateTime(s.expires!).format(context);
    return [
      Center(child: SizedBox.square(dimension: 240, child: QrView(key: const Key('web-share-qr'), data: s.url))),
      const SizedBox(height: 8),
      Text(l.webShareScan, textAlign: TextAlign.center, style: const TextStyle(fontWeight: FontWeight.w600)),
      const SizedBox(height: 12),
      Text(l.webShareOrType(s.address), textAlign: TextAlign.center, style: TextStyle(color: c.muted)),
      const SizedBox(height: 4),
      SelectableText(
        s.pinShown,
        key: const Key('web-share-pin'),
        textAlign: TextAlign.center,
        style: const TextStyle(fontSize: 32, fontWeight: FontWeight.w800, letterSpacing: 4),
      ),
      if (hotspot != null) ...[
        const SizedBox(height: 8),
        Text(l.webShareNetwork(hotspot.ssid ?? '', hotspot.passphrase ?? ''), textAlign: TextAlign.center),
      ],
      const SizedBox(height: 16),
      // What the guest is offered, and more to add (real test of 10 November 2026).
      Text(l.webShareOffered, style: const TextStyle(fontWeight: FontWeight.w700)),
      const SizedBox(height: 4),
      if (_ws.offered.isEmpty)
        Text(l.webShareNothingOffered, style: TextStyle(color: c.muted, fontSize: 13))
      else ...[
        for (final n in _ws.offered.take(5)) Text(n, key: ValueKey('web-share-offered-$n'), style: TextStyle(color: c.muted, fontSize: 13)),
        if (_ws.offered.length > 5) Text(l.webShareOfferedMore(_ws.offered.length - 5), style: TextStyle(color: c.muted, fontSize: 13)),
      ],
      const SizedBox(height: 6),
      Wrap(
        spacing: 8,
        runSpacing: 8,
        children: [
          if (picker.supported) OutlinedButton.icon(key: const Key('web-share-add-files'), onPressed: () => unawaited(_addFiles()), icon: const Icon(Icons.add_rounded), label: Text(l.webShareAddFiles)),
          if (picker.folders) OutlinedButton.icon(key: const Key('web-share-add-folder'), onPressed: () => unawaited(_addFolder()), icon: const Icon(Icons.create_new_folder_outlined), label: Text(l.webShareAddFolder)),
        ],
      ),
      if (_ws.error case final error?) Text(l.webShareFailed(error), style: TextStyle(color: c.danger, fontSize: 13)),
      const SizedBox(height: 16),
      // The warning every guest meets: said before it surprises (gate G5).
      Container(
        key: const Key('web-share-warning'),
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(border: Border.all(color: c.border), borderRadius: BorderRadius.circular(12)),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(l.webShareWarning, style: const TextStyle(fontSize: 13, height: 1.4)),
            const SizedBox(height: 6),
            GestureDetector(
              onLongPress: () => unawaited(Clipboard.setData(ClipboardData(text: s.fingerprint))),
              child: Text(l.webShareFingerprint(s.fingerprint), style: TextStyle(fontSize: 11, color: c.muted, fontFamily: 'monospace')),
            ),
          ],
        ),
      ),
      if (up != null) ...[
        const SizedBox(height: 16),
        Container(
          key: const Key('web-share-upload'),
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(color: c.surface, border: Border.all(color: c.border), borderRadius: BorderRadius.circular(12)),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(l.webShareAsks(up.files.length, formatBytes(context, up.total)), style: const TextStyle(fontWeight: FontWeight.w700)),
              const SizedBox(height: 6),
              for (final f in up.files.take(5)) Text('${f.name} · ${formatBytes(context, f.size)}', style: TextStyle(color: c.muted, fontSize: 13)),
              if (up.files.length > 5) Text('…', style: TextStyle(color: c.muted)),
              const SizedBox(height: 8),
              if (!up.accepted)
                Row(
                  children: [
                    Expanded(child: OutlinedButton(key: const Key('web-share-refuse'), onPressed: () => unawaited(_ws.answer(false)), child: Text(l.webShareRefuse))),
                    const SizedBox(width: 8),
                    Expanded(child: FilledButton(key: const Key('web-share-accept'), onPressed: () => unawaited(_ws.answer(true)), child: Text(l.webShareAccept))),
                  ],
                )
              else ...[
                LinearProgressIndicator(value: up.total > 0 ? up.received / up.total : null),
                const SizedBox(height: 6),
                Text(l.webShareReceiving(formatBytes(context, up.received), formatBytes(context, up.total)), style: TextStyle(color: c.muted, fontSize: 13)),
                Align(
                  alignment: Alignment.centerRight,
                  child: TextButton(key: const Key('web-share-stop-upload'), onPressed: () => unawaited(_ws.cancelUpload()), child: Text(l.webShareStopUpload)),
                ),
              ],
            ],
          ),
        ),
      ],
      if (_ws.received.isNotEmpty || _ws.downloads > 0) ...[
        const SizedBox(height: 16),
        if (_ws.received.isNotEmpty) ...[
          Text(l.webShareReceived(_ws.received.length), key: const Key('web-share-received'), style: const TextStyle(fontWeight: FontWeight.w700)),
          for (final n in _ws.received.reversed.take(10)) Text(n, style: TextStyle(color: c.muted, fontSize: 13)),
        ],
        if (_ws.downloads > 0) Text(l.webShareDownloads(_ws.downloads), key: const Key('web-share-downloads')),
      ],
      if (time != null) ...[
        const SizedBox(height: 16),
        Text(l.webShareExpires(time), style: TextStyle(color: c.muted, fontSize: 13)),
      ],
      const SizedBox(height: 16),
      OutlinedButton.icon(
        key: const Key('web-share-stop'),
        style: OutlinedButton.styleFrom(foregroundColor: c.danger, minimumSize: const Size.fromHeight(52)),
        onPressed: () => unawaited(_ws.stop()),
        icon: const Icon(Icons.stop_circle_outlined),
        label: Text(l.webShareStop),
      ),
    ];
  }
}
