/// Accueil, on a desktop (mockup « 10. Bureau »): files and folders dropped on the window or
/// chosen, as many times as wanted, fill one list, sent in one transfer to the device connected
/// (WP-B.8: the system's dialog chooses files or a folder, never both). Beside it, the
/// connection, as on the phone (decided on 27 October 2026): the devices of the same network,
/// which see this one too; else this one creates its own network (WP-C.3 of the J6c plan), or
/// joins another's. Connecting
/// says nothing of who sends: once connected, either device does.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../app_state.dart';
import '../../connection/cable_watch.dart';
import '../../connection/connection.dart';
import '../../l10n/app_localizations.dart';
import '../../library/desktop_picks.dart';
import '../../library/selection.dart';
import '../../theme.dart';
import '../../widgets/dashed_box.dart';
import '../../widgets/drop_zone.dart';
import '../../widgets/format.dart';
import '../connect/connect_screen.dart';
import '../connect/dialogs.dart';
import '../connect/hotspot_screen.dart';

class SendPage extends StatefulWidget {
  const SendPage({super.key});

  @override
  State<SendPage> createState() => _SendPageState();
}

class _SendPageState extends State<SendPage> {
  late final AppState _state = AppScope.of(context, listen: false).state;

  void _add(List<String> paths) {
    _state.selection.addAll(picksOfPaths(paths));
    _state.explorer.sizeFolders(_state.selection);
  }

  Future<void> _chooseFiles() async => _add((await _state.filePicker.files()).paths);

  Future<void> _chooseFolder() async {
    final folder = await _state.filePicker.folder();
    if (folder != null) _add([folder]);
  }

  /// Everything chosen, by path, in one transfer, to the device connected.
  Future<void> _send() async {
    final sel = _state.selection;
    if (sel.isEmpty || _state.connection.session == null) return;
    final plan = sel.plan(byPath: true) as ByPath;
    await _state.transfers.send(plan.paths);
    sel.clear();
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    return DropZone(
      hint: l.dropHere,
      onPaths: _add,
      child: ListenableBuilder(
        listenable: Listenable.merge([_state.connection, _state.selection, _state.cable]),
        builder: (context, _) => LayoutBuilder(
          builder: (context, box) {
            final pick = _Pick(onFiles: _chooseFiles, onFolder: _chooseFolder, selection: _state.selection, session: _state.connection.session, onSend: _send);
            final connection = _ConnectionPanel(connection: _state.connection, cable: _state.cable);
            return SingleChildScrollView(
              padding: const EdgeInsets.all(24),
              child: box.maxWidth >= 860
                  ? Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Expanded(flex: 3, child: pick),
                        const SizedBox(width: 24),
                        Expanded(flex: 2, child: connection),
                      ],
                    )
                  : Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [pick, const SizedBox(height: 24), connection]),
            );
          },
        ),
      ),
    );
  }
}

/// The left column: where to drop or choose, the list of what was chosen, and send.
class _Pick extends StatelessWidget {
  const _Pick({required this.onFiles, required this.onFolder, required this.selection, required this.session, required this.onSend});

  final VoidCallback onFiles;
  final VoidCallback onFolder;
  final Selection selection;
  final PeerSession? session;
  final VoidCallback onSend;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final accent = Theme.of(context).colorScheme.primary;
    final choose = OutlinedButton.styleFrom(
      minimumSize: const Size(0, 44),
      padding: const EdgeInsets.symmetric(horizontal: 18),
      shape: const StadiumBorder(),
      backgroundColor: c.background,
      foregroundColor: c.text,
      side: BorderSide(color: c.border),
      textStyle: const TextStyle(fontFamily: 'Figtree', fontWeight: FontWeight.w600),
    );
    final picks = selection.items.toList();
    final to = session;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(l.sendSelection, style: const TextStyle(fontSize: 24, fontWeight: FontWeight.w800)),
        const SizedBox(height: 16),
        DashedBox(
          key: const Key('drop-card'),
          color: accent.withValues(alpha: 0.4),
          fill: c.surface,
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 40, horizontal: 24),
            child: Column(
              children: [
                Icon(Icons.upload_rounded, size: 40, color: accent),
                const SizedBox(height: 12),
                Text(
                  l.dropTitle,
                  textAlign: TextAlign.center,
                  style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w700),
                ),
                const SizedBox(height: 12),
                Text(
                  l.dropBody,
                  textAlign: TextAlign.center,
                  style: TextStyle(fontSize: 14, color: c.muted),
                ),
                const SizedBox(height: 12),
                Wrap(
                  alignment: WrapAlignment.center,
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    OutlinedButton(key: const Key('pick-files'), style: choose, onPressed: onFiles, child: Text(l.chooseFiles)),
                    OutlinedButton(key: const Key('pick-folder'), style: choose, onPressed: onFolder, child: Text(l.chooseFolder)),
                  ],
                ),
              ],
            ),
          ),
        ),
        if (picks.isNotEmpty) ...[
          const SizedBox(height: 16),
          Material(
            key: const Key('basket'),
            color: c.surface,
            clipBehavior: Clip.antiAlias,
            shape: RoundedRectangleBorder(
              side: BorderSide(color: c.border),
              borderRadius: BorderRadius.circular(18),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 4, 8, 4),
                  child: Row(
                    children: [
                      Expanded(
                        child: Text(
                          l.basketLine(selection.count, formatBytes(context, selection.bytes)),
                          key: const Key('basket-line'),
                          style: const TextStyle(fontWeight: FontWeight.w700),
                        ),
                      ),
                      TextButton(key: const Key('basket-clear'), onPressed: selection.clear, child: Text(l.removeAll)),
                    ],
                  ),
                ),
                for (final (i, p) in picks.indexed) ...[
                  Divider(height: 1, color: c.border),
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 4, 4, 4),
                    child: Row(
                      children: [
                        Icon(p.kind == PickKind.folder ? Icons.folder_outlined : Icons.insert_drive_file_outlined, size: 20, color: c.muted),
                        const SizedBox(width: 12),
                        Expanded(
                          child: Text(
                            p.name,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 14),
                          ),
                        ),
                        Text(p.kind == PickKind.folder && p.size == 0 ? '…' : formatBytes(context, p.size), style: TextStyle(fontSize: 13, color: c.muted)),
                        IconButton(
                          key: ValueKey('basket-remove-$i'),
                          tooltip: l.removeOne,
                          icon: Icon(Icons.close_rounded, size: 18, color: c.muted),
                          onPressed: () => selection.toggle(p),
                        ),
                      ],
                    ),
                  ),
                ],
              ],
            ),
          ),
          const SizedBox(height: 16),
          if (to != null)
            FilledButton.icon(
              key: const Key('send'),
              style: FilledButton.styleFrom(
                minimumSize: const Size(0, 52),
                shape: const StadiumBorder(),
                textStyle: const TextStyle(fontFamily: 'Figtree', fontWeight: FontWeight.w800, fontSize: 16),
              ),
              onPressed: onSend,
              icon: const Icon(Icons.send_rounded),
              label: Text(l.sendToDevice(to.name)),
            )
          else
            Text(
              l.connectToSend,
              key: const Key('connect-to-send'),
              textAlign: TextAlign.center,
              style: TextStyle(color: c.muted),
            ),
        ],
      ],
    );
  }
}

/// The right column, the phone's way of connecting: the device connected; those of the same
/// network, which see this one too; else create a connection, or join another's.
class _ConnectionPanel extends StatelessWidget {
  const _ConnectionPanel({required this.connection, required this.cable});

  final Connection connection;

  /// The wired cards: an address given by the app (Linux), a sharing's address left (Windows).
  final CableWatch cable;

  void _create(BuildContext context) => unawaited(
    showDialog<void>(
      context: context,
      builder: (_) => Dialog(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 420),
          child: Padding(
            padding: const EdgeInsets.only(top: 24),
            child: ManualCodeSheet(connection: connection),
          ),
        ),
      ),
    ),
  );

  Future<void> _typePin(BuildContext context) async {
    final answer = await askPin(context);
    if (answer != null) await connection.pairPin(answer.$1, answer.$2);
  }

  Future<void> _pasteLink(BuildContext context) async {
    final link = await askLink(context);
    if (link != null && context.mounted) await joinByLink(context, link);
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final session = connection.session;
    final others = [
      for (final d in connection.nearby)
        if (session == null || (d.ip != session.ip && d.name != session.name)) d,
    ];
    final secondary = OutlinedButton.styleFrom(
      minimumSize: const Size(0, 44),
      padding: const EdgeInsets.symmetric(horizontal: 14),
      shape: const StadiumBorder(),
      foregroundColor: c.text,
      side: BorderSide(color: c.border),
      textStyle: const TextStyle(fontFamily: 'Figtree', fontWeight: FontWeight.w600),
    );
    Widget heading(String text) => Padding(
      padding: const EdgeInsets.fromLTRB(4, 16, 4, 8),
      child: Text(
        text.toUpperCase(),
        style: TextStyle(color: c.muted, fontSize: 13, fontWeight: FontWeight.w700, letterSpacing: 0.8),
      ),
    );
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(l.connectionTitle, style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w800)),
        const SizedBox(height: 12),
        if (session != null)
          _Device(
            key: const Key('device-session'),
            name: session.name,
            detail: connection.reconnecting ? l.reconnecting : l.deviceConnected,
            action: l.disconnect,
            primary: false,
            onAction: () => unawaited(connection.disconnect()),
          ),
        Row(
          children: [
            Expanded(child: heading(l.nearbyTitle)),
            connection.scanning
                ? const Padding(
                    padding: EdgeInsets.all(12),
                    child: SizedBox.square(dimension: 18, child: CircularProgressIndicator(strokeWidth: 2)),
                  )
                : IconButton(tooltip: l.refresh, icon: const Icon(Icons.refresh_rounded), onPressed: () => unawaited(connection.scan())),
          ],
        ),
        // The name the other user looks for in their list (DISC-07).
        if (connection.instance.isNotEmpty)
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 0, 4, 10),
            child: Text(
              l.youAppearAs(connection.instance),
              key: const Key('my-instance'),
              style: const TextStyle(fontWeight: FontWeight.w600),
            ),
          ),
        // DISC-13: a device reached by a cable says so; reached by a cable and by Wi-Fi, both
        // are offered and the user chooses (decision E2 of the cable plan).
        for (final d in others)
          if (d.paths.length > 1)
            _Device(
              key: ValueKey('device-${d.ip}'),
              name: d.name,
              detail: '${d.trusted ? l.nearbyTrusted : l.nearbyAsksPin}\n${l.pathChoose}',
              choices: [
                for (final p in d.paths)
                  (key: Key('device-${d.ip}-${p.kind.name}-${p.address}'), label: pathLabel(l, p.kind), onAction: () => unawaited(connection.pairWith(d.via(p)))),
              ],
            )
          else
            _Device(
              key: ValueKey('device-${d.ip}'),
              name: d.name,
              detail: [d.trusted ? l.nearbyTrusted : l.nearbyAsksPin, if (d.kind == PathKind.cable) l.byCable].join(' · '),
              action: l.connect,
              primary: true,
              onAction: () => unawaited(connection.pairWith(d)),
            ),
        if (others.isEmpty)
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 0, 4, 8),
            child: Text(connection.scanning ? l.searching : l.nearbyEmpty, style: TextStyle(color: c.muted)),
          ),
        for (final card in cable.given)
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 0, 4, 8),
            child: Text(l.cableGiven(card), key: Key('cable-given-$card'), style: TextStyle(color: c.muted, fontSize: 13)),
          ),
        if (cable.sharingLeftOn case final card?)
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 0, 4, 8),
            child: Text(l.cableSharingLeft(card), key: const Key('cable-sharing-left'), style: TextStyle(color: c.danger, fontSize: 13)),
          ),
        // Discovery blocked by the Wi-Fi or a firewall: this computer's address and PIN.
        Align(
          alignment: Alignment.centerLeft,
          child: TextButton(key: const Key('not-listed'), onPressed: () => _create(context), child: Text(l.notListed)),
        ),
        heading(l.otherNetwork),
        // This computer creates the network (WP-C.3 of the J6c plan): its QR code carries it.
        if (connection.hotspot.supported) ...[
          FilledButton.icon(
            key: const Key('hotspot-start'),
            style: FilledButton.styleFrom(
              minimumSize: const Size(0, 44),
              shape: const StadiumBorder(),
              textStyle: const TextStyle(fontFamily: 'Figtree', fontWeight: FontWeight.w700),
            ),
            onPressed: connection.hotspotBusy
                ? null
                : () => unawaited(Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => const HotspotScreen()))),
            icon: const Icon(Icons.wifi_tethering_rounded),
            label: Text(l.hotspotStart),
          ),
          if (connection.hotspot.leavesWifi)
            Padding(
              padding: const EdgeInsets.fromLTRB(4, 6, 4, 0),
              child: Text(l.hotspotLeavesWifi, key: const Key('hotspot-leaves-wifi'), style: TextStyle(color: c.muted, fontSize: 12)),
            ),
          const SizedBox(height: 8),
        ],
        Row(
          children: [
            Expanded(
              child: OutlinedButton(key: const Key('type-pin'), style: secondary, onPressed: () => unawaited(_typePin(context)), child: Text(l.typePin)),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: OutlinedButton(key: const Key('paste-link'), style: secondary, onPressed: () => unawaited(_pasteLink(context)), child: Text(l.pasteLink)),
            ),
          ],
        ),
      ],
    );
  }
}

/// One device of the list, with its button, or one button per path when it has several.
class _Device extends StatelessWidget {
  const _Device({
    super.key,
    required this.name,
    required this.detail,
    this.action = '',
    this.primary = true,
    this.onAction,
    this.choices = const [],
  });

  final String name;
  final String detail;
  final String action;
  final bool primary;
  final VoidCallback? onAction;
  final List<({Key key, String label, VoidCallback onAction})> choices;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final accent = Theme.of(context).colorScheme.primary;
    Widget button(String label, VoidCallback? onPressed, {required bool primary}) => FilledButton(
      style: FilledButton.styleFrom(
        minimumSize: const Size(0, 40),
        padding: const EdgeInsets.symmetric(horizontal: 16),
        shape: const StadiumBorder(),
        backgroundColor: primary ? accent : c.chip,
        foregroundColor: primary ? Colors.white : c.text,
        textStyle: const TextStyle(fontFamily: 'Figtree', fontWeight: FontWeight.w700),
      ),
      onPressed: onPressed,
      child: Text(label),
    );
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: c.surface,
          border: Border.all(color: c.border),
          borderRadius: BorderRadius.circular(16),
        ),
        child: Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(name, style: const TextStyle(fontWeight: FontWeight.w700, fontSize: 15)),
                  const SizedBox(height: 2),
                  Text(detail, style: TextStyle(color: c.muted, fontSize: 13)),
                ],
              ),
            ),
            const SizedBox(width: 12),
            if (choices.isEmpty)
              button(action, onAction, primary: primary)
            else
              Column(
                crossAxisAlignment: CrossAxisAlignment.end,
                mainAxisSize: MainAxisSize.min,
                children: [
                  for (final (i, ch) in choices.indexed)
                    Padding(
                      padding: EdgeInsets.only(top: i == 0 ? 0 : 6),
                      child: KeyedSubtree(key: ch.key, child: button(ch.label, ch.onAction, primary: i == 0)),
                    ),
                ],
              ),
          ],
        ),
      ),
    );
  }
}
