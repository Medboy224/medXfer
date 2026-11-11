/// « Partager avec un navigateur » (WP-W.5 of the J7 plan, bible chapter 11): before the
/// session, what the guest is offered and whether it may send; during it, the QR code, the PIN,
/// the browser's warning explained, what is offered, the guest's uploads to accept, and what
/// came. What the host chose lives in the model (WebShare), not here: the screen may be left and
/// found again as it was, and the session goes on meanwhile (real test of 10 November 2026). It
/// ends on « Arrêter le partage », at its expiry, or with the app (WEB-10).
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app.dart';
import '../../connection/connection.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import '../../transfer/web_share.dart';
import '../../widgets/format.dart';
import '../../widgets/qr_view.dart';

/// The home's selection to a browser (its « Navigateur » button): added to the session running,
/// said in a line; else listed on the screen, which [open] shows. The selection stays: the same
/// files may go to a device with medXfer too.
Future<void> offerSelectionToBrowser(BuildContext context, {required VoidCallback open}) async {
  final state = AppScope.of(context, listen: false).state;
  final l = AppLocalizations.of(context);
  final messenger = ScaffoldMessenger.of(context);
  final picks = state.selection.items.toList();
  if (picks.isEmpty) return;
  final ws = state.webShare;
  if (ws.session == null) {
    await ws.offerSelection(picks);
    open();
    return;
  }
  final ok = await ws.offerSelection(picks);
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
  late final Connection _connection = AppScope.of(context, listen: false).state.connection;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _connection; // read now: the context is no longer there in dispose
      _ws.shown();
      if (_ws.session == null) unawaited(_ws.loadNetworks());
    });
  }

  @override
  void dispose() {
    // A network made here for a share that never started goes with the screen; one that serves a
    // share stays (Connection.hotspotInUse). Not during the unmount: the model tells its listeners.
    final connection = _connection;
    if (_ws.session == null) scheduleMicrotask(() => unawaited(connection.releaseHotspot()));
    super.dispose();
  }

  Future<void> _pickFiles() async {
    final picked = await AppScope.of(context, listen: false).state.filePicker.files();
    if (picked.isEmpty) return;
    if (_ws.session != null) {
      await _ws.add(paths: picked.paths, files: picked.files);
    } else {
      _ws
        ..addPaths(picked.paths)
        ..addFiles(picked.files);
    }
  }

  Future<void> _pickFolder() async {
    final folder = await AppScope.of(context, listen: false).state.filePicker.folder();
    if (folder == null) return;
    if (_ws.session != null) {
      await _ws.add(paths: [folder]);
    } else {
      _ws.addPaths([folder]);
    }
  }

  Future<void> _start() async {
    final hotspot = AppScope.of(context, listen: false).state.connection.myHotspot?.address;
    // The networks of now: one may have gone since the screen opened.
    if (hotspot == null) await _ws.loadNetworks();
    await _ws.start(address: hotspot);
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
              // Above the system's gesture bar: « Arrêter le partage », the last line, was under
              // it on the Galaxy A57 (real test of 10 November 2026).
              padding: const EdgeInsets.all(16) + EdgeInsets.only(bottom: MediaQuery.paddingOf(context).bottom),
              children: _ws.session == null ? _setup(context) : _running(context, _ws.session!),
            ),
          ),
        ),
      ),
    );
  }

  /// One line of what is offered, with its ✕.
  Widget _line(BuildContext context, {required Key key, required String name, required bool folder, String? detail, required VoidCallback onRemove}) {
    final c = context.colors;
    final l = AppLocalizations.of(context);
    return Padding(
      key: key,
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        children: [
          Icon(folder ? Icons.folder_outlined : Icons.insert_drive_file_outlined, size: 18, color: c.muted),
          const SizedBox(width: 8),
          Expanded(child: Text(name, maxLines: 1, overflow: TextOverflow.ellipsis)),
          if (detail != null) Text(detail, style: TextStyle(color: c.muted, fontSize: 13)),
          IconButton(tooltip: l.removeOne, icon: Icon(Icons.close_rounded, size: 18, color: c.muted), onPressed: onRemove),
        ],
      ),
    );
  }

  /// Adding, before or during the session.
  Widget _addButtons(BuildContext context) {
    final l = AppLocalizations.of(context);
    final picker = AppScope.of(context).state.filePicker;
    return Wrap(
      spacing: 8,
      runSpacing: 8,
      children: [
        if (picker.supported)
          OutlinedButton.icon(key: const Key('web-share-files'), onPressed: () => unawaited(_pickFiles()), icon: const Icon(Icons.add_rounded), label: Text(l.webShareAddFiles)),
        if (picker.folders)
          OutlinedButton.icon(key: const Key('web-share-folder'), onPressed: () => unawaited(_pickFolder()), icon: const Icon(Icons.create_new_folder_outlined), label: Text(l.webShareAddFolder)),
      ],
    );
  }

  /// Before the session: what is offered, whether the guest may send, the network.
  List<Widget> _setup(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final connection = AppScope.of(context).state.connection;
    final draft = _ws.draft;
    final hotspot = connection.myHotspot;
    return [
      Text(l.webShareIntro, style: TextStyle(color: c.muted, height: 1.4)),
      if (_ws.ended case final ended? when ended != WebShareEnd.stopped) ...[
        const SizedBox(height: 12),
        Text(
          key: const Key('web-share-ended'),
          ended == WebShareEnd.expired ? l.webShareEndedExpired : l.webShareEndedPin,
          style: TextStyle(color: c.danger),
        ),
      ],
      const SizedBox(height: 20),
      Text(l.webShareOffered, style: const TextStyle(fontWeight: FontWeight.w700)),
      const SizedBox(height: 6),
      if (draft.isEmpty)
        Text(l.webShareNothingOffered, style: TextStyle(color: c.muted))
      else
        for (final (i, d) in draft.indexed)
          _line(
            context,
            key: ValueKey('web-share-draft-$i'),
            name: d.name,
            folder: d.folder,
            detail: d.pick != null && d.pick!.size > 0 ? formatBytes(context, d.pick!.size) : null,
            onRemove: () => unawaited(_ws.removeDraft(i)),
          ),
      const SizedBox(height: 8),
      _addButtons(context),
      const SizedBox(height: 12),
      SwitchListTile(
        key: const Key('web-share-receive'),
        contentPadding: EdgeInsets.zero,
        value: _ws.receive,
        onChanged: (v) => _ws.receive = v,
        title: Text(l.webShareReceive),
        subtitle: Text(l.webShareReceiveHint),
      ),
      SwitchListTile(
        key: const Key('web-share-auto'),
        contentPadding: EdgeInsets.zero,
        value: _ws.autoAccept,
        onChanged: _ws.receive ? (v) => _ws.autoAccept = v : null,
        title: Text(l.webShareAutoAccept),
      ),
      // The guest's network: never mobile data; chosen among several (real tests of 10 November
      // 2026: the core served on the mobile data, or on a cable where the guest was not).
      if (hotspot == null) ..._networkChoice(context),
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
        Text(_failure(l, error), style: TextStyle(color: c.danger)),
      ],
      const SizedBox(height: 16),
      FilledButton.icon(
        key: const Key('web-share-start'),
        style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(52)),
        onPressed: _ws.starting || connection.hotspotBusy || (draft.isEmpty && !_ws.receive) ? null : () => unawaited(_start()),
        icon: _ws.starting ? const SizedBox.square(dimension: 18, child: CircularProgressIndicator(strokeWidth: 2)) : const Icon(Icons.public_rounded),
        label: Text(l.webShareStart),
      ),
    ];
  }

  /// The core's reason in the host's words, when it has some.
  static String _failure(AppLocalizations l, String error) => switch (error) {
        'no_local_network' => l.webShareNoNetwork,
        'address_not_local' => l.webShareNetworkGone,
        _ => l.webShareFailed(error),
      };

  static String _kind(AppLocalizations l, String kind) => switch (kind) {
        'wifi' => l.webShareKindWifi,
        'ethernet' => l.webShareKindCable,
        _ => l.webShareKindOther,
      };

  /// The network the guest must be on, said in words: the same Wi-Fi, the same cable network.
  static String _sameNetwork(AppLocalizations l, String kind) => switch (kind) {
        'wifi' => l.webShareSameWifi,
        'ethernet' => l.webShareSameCable,
        _ => l.webShareSameNetwork,
      };

  /// Before the session, without this device's own network: the networks the guest may be on.
  /// None: said (mobile data never serves). One: said. Several: the host chooses.
  List<Widget> _networkChoice(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final nets = _ws.networks;
    final chosen = _ws.network;
    final refresh = IconButton(
      key: const Key('web-share-networks-refresh'),
      tooltip: l.webShareNetworksRefresh,
      icon: const Icon(Icons.refresh_rounded, size: 20),
      onPressed: () => unawaited(_ws.loadNetworks()),
    );
    if (nets.isEmpty) {
      return [
        Row(children: [
          Expanded(child: Text(l.webShareNoNetwork, key: const Key('web-share-no-network'), style: TextStyle(color: c.danger))),
          refresh,
        ]),
      ];
    }
    final net = nets.firstWhere((n) => n.ip == chosen, orElse: () => nets.first);
    return [
      const SizedBox(height: 8),
      Row(children: [
        Expanded(child: Text(l.webShareGuestNetwork, style: const TextStyle(fontWeight: FontWeight.w700))),
        refresh,
      ]),
      if (nets.length == 1)
        Text('${_kind(l, net.kind)} · ${net.ip}', key: const Key('web-share-network-one'))
      else
        RadioGroup<String>(
          groupValue: chosen,
          onChanged: (ip) => _ws.network = ip,
          child: Column(
            children: [
              for (final n in nets)
                RadioListTile<String>(
                  key: ValueKey('web-share-network-${n.ip}'),
                  contentPadding: EdgeInsets.zero,
                  dense: true,
                  value: n.ip,
                  title: Text('${_kind(l, n.kind)} · ${n.ip}'),
                ),
            ],
          ),
        ),
      Text(_sameNetwork(l, net.kind), style: TextStyle(color: c.muted, fontSize: 13)),
    ];
  }

  /// During the session: how the guest comes in, what is offered, what it asks, what came.
  List<Widget> _running(BuildContext context, WebShareSession s) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final hotspot = AppScope.of(context).state.connection.myHotspot;
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
      const SizedBox(height: 8),
      if (hotspot != null)
        Text(l.webShareNetwork(hotspot.ssid ?? '', hotspot.passphrase ?? ''), textAlign: TextAlign.center)
      else
        Text(_sameNetwork(l, s.kind), key: const Key('web-share-same-network'), textAlign: TextAlign.center, style: TextStyle(color: c.muted)),
      const SizedBox(height: 16),
      // What the guest is offered: added or withdrawn at any time (real test of 10 November 2026).
      Text(l.webShareOffered, style: const TextStyle(fontWeight: FontWeight.w700)),
      const SizedBox(height: 4),
      if (_ws.items.isEmpty)
        Text(l.webShareNothingOffered, style: TextStyle(color: c.muted, fontSize: 13))
      else
        for (final it in _ws.items)
          _line(
            context,
            key: ValueKey('web-share-item-${it.id}'),
            name: it.name,
            folder: it.folder,
            detail: it.size > 0 ? formatBytes(context, it.size) : null,
            onRemove: () => unawaited(_ws.remove(it.id)),
          ),
      const SizedBox(height: 6),
      _addButtons(context),
      if (_ws.error case final error?) Text(_failure(l, error), style: TextStyle(color: c.danger, fontSize: 13)),
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

/// The session running, wherever the user is (real test of 10 November 2026: leaving the screen
/// must not lose it): one line, its code, a tap back to its screen.
class WebShareBanner extends StatelessWidget {
  const WebShareBanner({super.key, required this.onOpen});

  final VoidCallback onOpen;

  @override
  Widget build(BuildContext context) {
    final ws = AppScope.of(context).state.webShare;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final accent = Theme.of(context).colorScheme.primary;
    return ListenableBuilder(
      listenable: ws,
      builder: (context, _) {
        final s = ws.session;
        if (s == null) return const SizedBox.shrink();
        return Material(
          color: c.chip,
          child: InkWell(
            key: const Key('web-share-banner'),
            onTap: onOpen,
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
              // Narrow (the desktop's side menu): the code under the line, not squeezed beside it.
              child: LayoutBuilder(
                builder: (context, box) {
                  final title = Text(l.webShareRunning, style: const TextStyle(fontWeight: FontWeight.w700));
                  final code = Text(l.webShareRunningPin(s.pinShown), style: TextStyle(color: c.muted));
                  return Row(
                    children: [
                      Icon(Icons.public_rounded, size: 20, color: accent),
                      const SizedBox(width: 10),
                      if (box.maxWidth < 320)
                        Expanded(child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [title, code]))
                      else ...[
                        Expanded(child: title),
                        code,
                      ],
                      Icon(Icons.chevron_right_rounded, color: c.muted),
                    ],
                  );
                },
              ),
            ),
          ),
        );
      },
    );
  }
}
