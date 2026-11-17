/// The Connect screen, « Version 4 » (plan J8, WP-8.1): first the devices already on the same
/// network, then, if none, create a network or join one. Create an access point (Android, WP-B.7): its QR code carries the network, so
/// scanning it works wherever the devices are. Join a device that created one: scan its QR code
/// or type its PIN. Or pick a device already on the same Wi-Fi. No QR code of the shared network
/// here: one scanned elsewhere would fail. A device missing from the list is reached by its
/// address and PIN. Once paired, the screen closes: the banner of the home screen takes over.
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app.dart';
import '../../connection/ble_link.dart';
import '../../connection/connection.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import 'ble_nearby.dart';
import 'dialogs.dart';
import 'hotspot_screen.dart';

class ConnectScreen extends StatefulWidget {
  const ConnectScreen({super.key});

  @override
  State<ConnectScreen> createState() => _ConnectScreenState();
}

class _ConnectScreenState extends State<ConnectScreen> {
  late final Connection _c;
  Timer? _rescan;
  String? _pinFor; // the name of the device whose PIN is asked for
  bool _asking = false;

  @override
  void initState() {
    super.initState();
    _c = AppScope.of(context, listen: false).state.connection;
    _c.addListener(_changed);
    unawaited(_c.openConnect());
    // The devices come and go: look again every 5 s, a scan lasting 2 s, while the screen is open.
    _rescan = Timer.periodic(const Duration(seconds: 5), (_) {
      if (!_c.scanning) unawaited(_c.scan());
    });
  }

  @override
  void dispose() {
    _rescan?.cancel();
    _c.removeListener(_changed);
    // Not during the unmount: the model tells its listeners, and the tree is locked now.
    scheduleMicrotask(() => unawaited(_c.closeConnect()));
    super.dispose();
  }

  void _changed() {
    if (!mounted) return;
    if (_c.session != null) {
      // This screen's own route: the top one may be a window closing (the PIN's), and popping
      // it would leave this screen open.
      final route = ModalRoute.of(context);
      if (route != null && route.isActive) {
        route.isCurrent ? Navigator.of(context).pop() : Navigator.of(context).removeRoute(route);
      }
      return;
    }
    final target = _c.pinNeeded;
    if (target != null && !_asking) unawaited(_askPin(target));
  }

  Future<void> _askPin(String? target) async {
    _asking = true;
    final answer = await askPin(context, target: target, device: _pinFor ?? target);
    _asking = false;
    _pinFor = null;
    if (answer == null) {
      _c.cancelPinNeeded();
      return;
    }
    await _c.pairPin(answer.$1, answer.$2);
  }

  Future<void> _pasteLink() async {
    final link = await askLink(context);
    if (link != null && mounted) await joinByLink(context, link);
  }

  void _openHotspot() => unawaited(Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => const HotspotScreen())));

  void _openManual() => unawaited(
    showModalBottomSheet<void>(context: context, isScrollControlled: true, showDragHandle: true, builder: (_) => ManualCodeSheet(connection: _c)),
  );

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final scanner = AppScope.of(context).state.qrScanner;
    final ble = AppScope.of(context).state.bleLink;
    return Scaffold(
      appBar: AppBar(
        title: ListenableBuilder(
          listenable: _c,
          builder: (context, _) => Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(l.connectTitle),
              // The name the other user looks for in their list (DISC-07): a device not yet
              // trusted never sees this one's real name.
              if (_c.instance.isNotEmpty)
                Text(
                  l.youAppearAs(_c.instance),
                  key: const Key('my-instance'),
                  style: TextStyle(fontSize: 13, fontWeight: FontWeight.w400, color: context.colors.muted),
                ),
            ],
          ),
        ),
      ),
      body: BleFlow(
        link: ble,
        child: ListenableBuilder(
          listenable: Listenable.merge([_c, ble]),
          builder: (context, _) => Align(
            alignment: Alignment.topCenter,
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 560),
              child: ListView(
                padding: const EdgeInsets.fromLTRB(12, 4, 12, 20),
                children: [
                  _Nearby(
                    connection: _c,
                    ble: ble,
                    onPick: (d, {only = false}) {
                      _pinFor = d.name;
                      unawaited(_c.pairWith(d, only: only));
                    },
                    onNotListed: _openManual,
                  ),
                  const SizedBox(height: 10),
                  Padding(padding: const EdgeInsets.fromLTRB(4, 0, 4, 10), child: Text(l.notSameNetwork, style: titleStyle(15))),
                  if (_c.hotspot.supported) ...[_HotspotCard(busy: _c.hotspotBusy, onOpen: _openHotspot), const SizedBox(height: 10)],
                  _JoinCard(onScan: scanner.supported ? () => scanAndJoin(context) : null, onPaste: _pasteLink, onPin: () => _askPin(null)),
                  const SizedBox(height: 10),
                  Material(
                    color: context.colors.surface,
                    borderRadius: BorderRadius.circular(cardRadius),
                    clipBehavior: Clip.antiAlias,
                    child: SwitchListTile(
                      key: const Key('remember'),
                      value: _c.remember,
                      onChanged: (v) => unawaited(_c.setRemember(v)),
                      title: Text(l.rememberDevice, style: const TextStyle(fontWeight: FontWeight.w700)),
                      subtitle: Text(l.rememberHint),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// A card of the screen, with its title.
class _Section extends StatelessWidget {
  const _Section({required this.title, required this.child});

  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    // A Material, not a coloured box: the buttons inside paint their ink on it.
    return Material(
      color: c.surface,
      borderRadius: BorderRadius.circular(cardRadius),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(14, 14, 12, 12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(title, style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 16)),
            const SizedBox(height: 4),
            child,
          ],
        ),
      ),
    );
  }
}

/// Create an access point: a card with its sign and what it does.
class _HotspotCard extends StatelessWidget {
  const _HotspotCard({required this.busy, required this.onOpen});

  final bool busy;
  final VoidCallback onOpen;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return Material(
      color: c.surface,
      borderRadius: BorderRadius.circular(cardRadius),
      child: InkWell(
        key: const Key('hotspot-start'),
        borderRadius: BorderRadius.circular(cardRadius),
        onTap: busy ? null : onOpen,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(12, 14, 10, 14),
          child: Row(
            children: [
              Container(
                width: 48,
                height: 48,
                decoration: BoxDecoration(color: c.soft, borderRadius: BorderRadius.circular(controlRadius)),
                child: Icon(Icons.wifi_tethering_rounded, color: c.accent, size: 26),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(l.hotspotStart, style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w800)),
                    const SizedBox(height: 4),
                    Text(l.hotspotCardHint, style: TextStyle(color: c.muted, fontSize: 13, height: 1.35)),
                  ],
                ),
              ),
              busy
                  ? Padding(padding: const EdgeInsets.all(4), child: SizedBox.square(dimension: 20, child: CircularProgressIndicator(strokeWidth: 2.5, color: c.accent)))
                  : const Icon(Icons.chevron_right_rounded),
            ],
          ),
        ),
      ),
    );
  }
}

/// Join a device that created an access point: its QR code, or its Wi-Fi then its PIN.
class _JoinCard extends StatelessWidget {
  const _JoinCard({required this.onPin, this.onScan, this.onPaste});

  final VoidCallback? onScan;
  final VoidCallback? onPaste;
  final VoidCallback onPin;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return _Section(
      title: l.joinTitle,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(l.joinHint, style: TextStyle(color: c.muted, height: 1.4)),
          const SizedBox(height: 12),
          GridView.count(
            crossAxisCount: [onScan, onPaste].where((x) => x != null).length + 1,
            shrinkWrap: true,
            physics: const NeverScrollableScrollPhysics(),
            mainAxisSpacing: 8,
            crossAxisSpacing: 8,
            childAspectRatio: 1.5,
            children: [
              if (onScan != null) _JoinTile(key: const Key('scan-qr'), icon: Icons.qr_code_scanner_rounded, label: l.scanQrShort, onTap: onScan!),
              _JoinTile(key: const Key('type-pin'), icon: Icons.dialpad_rounded, label: l.typePin, onTap: onPin),
              if (onPaste != null) _JoinTile(key: const Key('paste-link'), icon: Icons.content_paste_rounded, label: l.pasteLink, onTap: onPaste!),
            ],
          ),
        ],
      ),
    );
  }
}

/// One way to join: its sign over its name.
class _JoinTile extends StatelessWidget {
  const _JoinTile({super.key, required this.icon, required this.label, required this.onTap});

  final IconData icon;
  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Material(
      color: c.section,
      borderRadius: BorderRadius.circular(controlRadius),
      child: InkWell(
        borderRadius: BorderRadius.circular(controlRadius),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.all(6),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Icon(icon, color: c.accent),
              const SizedBox(height: 6),
              Text(label, textAlign: TextAlign.center, maxLines: 2, style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w700)),
            ],
          ),
        ),
      ),
    );
  }
}

/// The sign of a kind of path.
IconData pathIcon(PathKind kind) => switch (kind) {
  PathKind.cable => Icons.cable_rounded,
  PathKind.wifi => Icons.wifi_rounded,
  PathKind.other => Icons.lan_outlined,
};

/// A small label: how a device is reached (Wi-Fi, Câble, Bluetooth).
class PathChip extends StatelessWidget {
  const PathChip({super.key, required this.icon, required this.label});

  final IconData icon;
  final String label;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Container(
      padding: const EdgeInsets.fromLTRB(6, 2, 8, 2),
      decoration: BoxDecoration(color: c.section, borderRadius: BorderRadius.circular(8)),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 13),
          const SizedBox(width: 4),
          Flexible(child: Text(label, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w700))),
        ],
      ),
    );
  }
}

/// The name of a kind of path, as the screens show it (DISC-13).
String pathLabel(AppLocalizations l, PathKind kind) => switch (kind) {
  PathKind.cable => l.pathCable,
  PathKind.wifi => l.pathWifi,
  PathKind.other => l.pathOther,
};

/// The devices already on the same Wi-Fi, under the name this one appears as, and those found
/// by Bluetooth (WP-D.5).
class _Nearby extends StatelessWidget {
  const _Nearby({required this.connection, required this.ble, required this.onPick, required this.onNotListed});

  final Connection connection;
  final BleLink ble;
  /// Tapped: its paths in order; [only] the one of a path button.
  final void Function(NearbyDevice d, {bool only}) onPick;
  final VoidCallback onNotListed;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final devices = connection.nearby;
    final byBle = bleOnly(ble, connection);
    final busy = connection.connecting != null || bleBusy(ble);
    bool tried(NearbyDevice d) => connection.connecting == d.ip || d.paths.any((p) => p.address == connection.connecting);
    Widget card({required Key key, required VoidCallback? onTap, required IconData icon, required String name, required Widget chip, required String line, required Widget end, List<Widget> extra = const []}) =>
        Padding(
          padding: const EdgeInsets.only(bottom: 8),
          child: Material(
            color: c.surface,
            borderRadius: BorderRadius.circular(cardRadius),
            child: InkWell(
              key: key,
              borderRadius: BorderRadius.circular(cardRadius),
              onTap: onTap,
              child: ConstrainedBox(
                constraints: const BoxConstraints(minHeight: 76),
                child: Padding(
                  padding: const EdgeInsets.fromLTRB(12, 10, 10, 10),
                  child: Row(
                    children: [
                      Container(
                        width: 48,
                        height: 48,
                        decoration: BoxDecoration(color: c.section, borderRadius: BorderRadius.circular(controlRadius)),
                        child: Icon(icon, color: c.text),
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Wrap(
                              spacing: 8,
                              runSpacing: 4,
                              crossAxisAlignment: WrapCrossAlignment.center,
                              children: [Text(name, maxLines: 2, overflow: TextOverflow.ellipsis, style: const TextStyle(fontWeight: FontWeight.w700, fontSize: 16)), chip],
                            ),
                            const SizedBox(height: 4),
                            Text(line, style: TextStyle(color: c.muted, fontSize: 13, height: 1.35)),
                            ...extra,
                          ],
                        ),
                      ),
                      const SizedBox(width: 8),
                      end,
                    ],
                  ),
                ),
              ),
            ),
          ),
        );
    Widget pill(String text, {bool busy = false}) => Container(
      constraints: const BoxConstraints(minHeight: 44),
      padding: const EdgeInsets.symmetric(horizontal: 12),
      alignment: Alignment.center,
      decoration: BoxDecoration(color: busy ? c.section : c.strong, borderRadius: BorderRadius.circular(12)),
      child: Text(text, style: TextStyle(color: busy ? c.muted : c.onStrong, fontSize: 14, fontWeight: FontWeight.w800)),
    );
    final connectPill = pill(l.connect);
    Widget spinner() => SizedBox.square(key: const Key('ble-working'), dimension: 22, child: CircularProgressIndicator(strokeWidth: 2.5, color: c.accent));
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            Expanded(child: Padding(padding: const EdgeInsets.only(left: 4), child: Text(l.nearbyTitle, style: titleStyle(15)))),
            connection.scanning
                ? Padding(padding: const EdgeInsets.all(13), child: SizedBox.square(dimension: 18, child: CircularProgressIndicator(strokeWidth: 2.5, color: c.accent)))
                : IconButton(tooltip: l.refresh, icon: const Icon(Icons.refresh_rounded), onPressed: () => unawaited(connection.scan())),
          ],
        ),
        BleStatus(link: ble),
        if (devices.isEmpty && byBle.isEmpty)
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 4, 4, 8),
            child: Text(connection.scanning ? l.searching : l.nearbyEmpty, style: TextStyle(color: c.muted)),
          ),
        for (final d in devices)
          card(
            key: ValueKey('nearby-${d.ip}'),
            // One attempt at a time: the device tried says so (real test of 1 November).
            onTap: busy ? null : () => onPick(d),
            icon: d.trusted ? Icons.verified_user_rounded : Icons.devices_rounded,
            name: d.name,
            chip: PathChip(icon: pathIcon(d.kind), label: pathLabel(l, d.kind)),
            line: tried(d) ? l.connectingLabel : (d.trusted ? l.nearbyTrusted : l.nearbyAsksPin),
            // DISC-13: reached by a cable and by Wi-Fi, as on a desktop, the user may choose;
            // the card itself tries them in order (#263).
            extra: [
              if (d.paths.length > 1)
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: [
                      for (final p in d.paths)
                        OutlinedButton.icon(
                          key: Key('nearby-${d.ip}-${p.kind.name}-${p.address}'),
                          style: OutlinedButton.styleFrom(minimumSize: const Size(0, 44), padding: const EdgeInsets.symmetric(horizontal: 12)),
                          onPressed: busy ? null : () => onPick(d.via(p), only: true),
                          icon: Icon(pathIcon(p.kind), size: 18),
                          label: Text(connection.connecting == p.address ? l.connectingLabel : pathLabel(l, p.kind)),
                        ),
                    ],
                  ),
                ),
            ],
            // Said in words, not by a spinner: the line and the pill read « Connexion… ».
            end: tried(d) ? pill(l.connectingLabel, busy: true) : connectPill,
          ),
        // Found by Bluetooth, without a shared Wi-Fi (WP-D.5): touched, the network follows.
        for (final d in byBle)
          card(
            key: ValueKey('ble-${d.device}'),
            onTap: busy ? null : () => unawaited(ble.connect(d)),
            icon: d.trusted ? Icons.verified_user_rounded : Icons.smartphone_rounded,
            name: d.name,
            chip: PathChip(icon: Icons.bluetooth_rounded, label: l.pathBluetooth),
            line: bleDetail(l, ble, d),
            end: bleTouching(ble, d) ? spinner() : connectPill,
          ),
        Align(
          alignment: Alignment.centerLeft,
          child: TextButton(
            key: const Key('not-listed'),
            onPressed: onNotListed,
            style: TextButton.styleFrom(textStyle: const TextStyle(fontFamily: fontFamily, fontWeight: FontWeight.w700, fontSize: 14, decoration: TextDecoration.underline)),
            child: Text(l.notListed),
          ),
        ),
      ],
    );
  }
}

/// A device that does not see this one in its list (discovery blocked by the Wi-Fi or a
/// firewall) types this address and PIN. No QR code: the two must share the network.
class ManualCodeSheet extends StatefulWidget {
  const ManualCodeSheet({super.key, required this.connection});

  final Connection connection;

  @override
  State<ManualCodeSheet> createState() => _ManualCodeSheetState();
}

class _ManualCodeSheetState extends State<ManualCodeSheet> {
  Connection get _c => widget.connection;

  @override
  void initState() {
    super.initState();
    _c.addListener(_changed);
    unawaited(_c.openCode());
  }

  @override
  void dispose() {
    _c.removeListener(_changed);
    scheduleMicrotask(_c.closeCode);
    super.dispose();
  }

  void _changed() {
    if (mounted && _c.session != null) Navigator.of(context).pop();
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(24, 0, 24, 24),
        child: ListenableBuilder(
          listenable: _c,
          builder: (context, _) {
            final pin = _c.pin;
            final inv = _c.invitation;
            return Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text(l.manualTitle, style: titleStyle(20)),
                const SizedBox(height: 8),
                Text(l.manualBody, style: TextStyle(color: c.muted, height: 1.4)),
                const SizedBox(height: 16),
                if (pin == null)
                  const Center(child: Padding(padding: EdgeInsets.all(16), child: CircularProgressIndicator()))
                else if (pin.stopped)
                  Text(l.pinStopped, textAlign: TextAlign.center, style: TextStyle(color: c.danger))
                else ...[
                  // One address per network (DISC-13): the other PC types the one of the network
                  // they share, the cable's or the Wi-Fi's.
                  if (pin.addresses.length > 1)
                    for (final a in pin.addresses)
                      SelectableText(
                        l.pinAddressLine(pathLabel(l, a.kind), a.address),
                        key: Key('my-address-${a.kind.name}-${a.address}'),
                        textAlign: TextAlign.center,
                        style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w700),
                      )
                  else
                    SelectableText(pin.address, key: const Key('my-address'), textAlign: TextAlign.center, style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w700)),
                  const SizedBox(height: 4),
                  SelectableText(
                    pin.pin,
                    key: const Key('my-pin'),
                    textAlign: TextAlign.center,
                    style: titleStyle(40, weight: FontWeight.w900, color: c.accent).copyWith(letterSpacing: 2, fontFeatures: tabular),
                  ),
                  Center(child: CodeCountdown(until: pin.expires, onExpired: () => unawaited(_c.showCode()))),
                ],
                const SizedBox(height: 8),
                Wrap(
                  alignment: WrapAlignment.center,
                  children: [
                    TextButton.icon(onPressed: () => unawaited(_c.showCode()), icon: const Icon(Icons.refresh_rounded), label: Text(l.newCode)),
                    // On a desktop, the link pasted on the other one (same network).
                    if (inv != null && inv.uri.isNotEmpty)
                      TextButton.icon(
                        onPressed: () async {
                          await Clipboard.setData(ClipboardData(text: inv.uri));
                          if (!context.mounted) return;
                          ScaffoldMessenger.of(context)
                            ..clearSnackBars()
                            ..showSnackBar(SnackBar(content: Text(l.linkCopied)));
                        },
                        icon: const Icon(Icons.copy_rounded),
                        label: Text(l.copyLink),
                      ),
                  ],
                ),
              ],
            );
          },
        ),
      ),
    );
  }
}

/// "Valid for n s", then [onExpired] once: a new code is drawn. Its own widget: the QR code is
/// not redrawn each second.
class CodeCountdown extends StatefulWidget {
  const CodeCountdown({super.key, required this.until, required this.onExpired});

  final DateTime until;
  final VoidCallback onExpired;

  @override
  State<CodeCountdown> createState() => _CodeCountdownState();
}

class _CodeCountdownState extends State<CodeCountdown> {
  Timer? _tick;
  bool _fired = false;

  @override
  void initState() {
    super.initState();
    _tick = Timer.periodic(const Duration(seconds: 1), (_) => _check());
  }

  @override
  void didUpdateWidget(CodeCountdown old) {
    super.didUpdateWidget(old);
    if (old.until != widget.until) _fired = false;
  }

  void _check() {
    if (!mounted) return;
    if (!_fired && !DateTime.now().isBefore(widget.until)) {
      _fired = true;
      widget.onExpired();
    }
    setState(() {});
  }

  @override
  void dispose() {
    _tick?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final left = widget.until.difference(DateTime.now()).inSeconds.clamp(0, 9999);
    return Text(AppLocalizations.of(context).validFor('$left'), style: TextStyle(color: context.colors.muted, fontSize: 13));
  }
}
