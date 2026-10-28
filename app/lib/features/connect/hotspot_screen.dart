/// This device's access point (mockup « Point d'accès actif », DISC-12): created when the screen
/// opens, by the phone or, since J6c, by a computer. Its QR code carries the network and the pairing: the other device joins and connects
/// at once. Without a camera, three steps: join this Wi-Fi, come back to medXfer, type the
/// address and PIN. Leaving before anyone joined stops the access point; once paired, the
/// screen closes and the access point stays for the session.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../connection/connection.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import '../../widgets/qr_view.dart';
import 'connect_screen.dart';

class HotspotScreen extends StatefulWidget {
  const HotspotScreen({super.key});

  @override
  State<HotspotScreen> createState() => _HotspotScreenState();
}

class _HotspotScreenState extends State<HotspotScreen> {
  late final Connection _c;
  bool _pinTab = false;

  @override
  void initState() {
    super.initState();
    _c = AppScope.of(context, listen: false).state.connection;
    _c.addListener(_changed);
    // After the first frame: starting notifies the model's listeners, built at this moment.
    WidgetsBinding.instance.addPostFrameCallback((_) => unawaited(_start()));
  }

  Future<void> _start() async {
    await _c.startHotspot();
    if (!mounted) return;
    // Refused or failed: the notice says why, the Connect screen is back.
    if (_c.myHotspot == null) {
      Navigator.of(context).pop();
      return;
    }
    await _c.openCode(); // drawn once the access point exists: the QR code carries it
  }

  @override
  void dispose() {
    _c.removeListener(_changed);
    // Not during the unmount: the model tells its listeners, and the tree is locked now.
    scheduleMicrotask(() {
      _c.closeCode();
      if (_c.session == null) unawaited(_c.stopHotspot()); // nobody joined
    });
    super.dispose();
  }

  void _changed() {
    if (!mounted || _c.session == null) return;
    final route = ModalRoute.of(context);
    if (route != null && route.isActive) {
      route.isCurrent ? Navigator.of(context).pop() : Navigator.of(context).removeRoute(route);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(l.hotspotActive),
            Text(l.hotspotNoInternet, style: TextStyle(color: c.muted, fontSize: 13)),
          ],
        ),
      ),
      body: ListenableBuilder(
        listenable: _c,
        builder: (context, _) {
          final h = _c.myHotspot;
          final pin = _c.pin;
          if (h == null || _c.qrData == null || pin == null) {
            return Center(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [const CircularProgressIndicator(), const SizedBox(height: 16), Text(l.hotspotBusy, style: TextStyle(color: c.muted))],
              ),
            );
          }
          return Align(
            alignment: Alignment.topCenter,
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 560),
              child: ListView(
                padding: const EdgeInsets.fromLTRB(20, 12, 20, 20),
                children: [
                  SegmentedButton<bool>(
                    segments: [
                      ButtonSegment(value: false, label: Text(l.tabQr, key: const Key('tab-qr'))),
                      ButtonSegment(value: true, label: Text(l.tabPinNoCamera, key: const Key('tab-pin'))),
                    ],
                    selected: {_pinTab},
                    showSelectedIcon: false,
                    onSelectionChanged: (s) => setState(() => _pinTab = s.first),
                  ),
                  const SizedBox(height: 18),
                  if (!_pinTab) ...[
                    Center(
                      child: Container(
                        padding: const EdgeInsets.all(14),
                        decoration: BoxDecoration(color: Colors.white, borderRadius: BorderRadius.circular(20)),
                        child: SizedBox.square(dimension: 230, child: QrView(key: const Key('my-qr'), data: _c.qrData!)),
                      ),
                    ),
                    const SizedBox(height: 14),
                    Text(l.hotspotQrHint, textAlign: TextAlign.center, style: const TextStyle(fontSize: 15, height: 1.45)),
                  ] else ...[
                    _Step(
                      n: 1,
                      title: l.stepJoinWifi,
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          const SizedBox(height: 8),
                          _Field(label: l.networkLabel, value: h.ssid ?? '', valueKey: const Key('hotspot-ssid')),
                          _Field(label: l.passwordLabel, value: h.passphrase ?? '', valueKey: const Key('hotspot-password')),
                          const SizedBox(height: 4),
                          Text(_c.hotspot.canJoin ? l.chosenByAndroid : l.chosenByApp, style: TextStyle(color: c.muted, fontSize: 12)),
                        ],
                      ),
                    ),
                    _Step(n: 2, title: l.stepBack),
                    _Step(
                      n: 3,
                      title: l.stepTypePin,
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          const SizedBox(height: 6),
                          SelectableText(pin.address, key: const Key('my-address'), style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w700)),
                          SelectableText(pin.pin, key: const Key('my-pin'), style: const TextStyle(fontSize: 32, fontWeight: FontWeight.w800, letterSpacing: 4)),
                        ],
                      ),
                    ),
                  ],
                  const SizedBox(height: 8),
                  Center(child: CodeCountdown(until: pin.expires, onExpired: () => unawaited(_c.showCode()))),
                  const SizedBox(height: 12),
                  Row(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      const SizedBox.square(dimension: 16, child: CircularProgressIndicator(strokeWidth: 2)),
                      const SizedBox(width: 8),
                      Flexible(child: Text(l.waitingOther, style: TextStyle(color: c.muted))),
                    ],
                  ),
                  const SizedBox(height: 8),
                  SwitchListTile(
                    key: const Key('remember'),
                    value: _c.remember,
                    onChanged: (v) => unawaited(_c.setRemember(v)),
                    title: Text(l.rememberDevice),
                    subtitle: Text(l.rememberHint),
                  ),
                  const SizedBox(height: 8),
                  OutlinedButton.icon(
                    key: const Key('hotspot-stop'),
                    style: OutlinedButton.styleFrom(foregroundColor: c.danger, minimumSize: const Size.fromHeight(52)),
                    onPressed: () => Navigator.of(context).pop(), // leaving stops it (dispose)
                    icon: const Icon(Icons.portable_wifi_off_rounded),
                    label: Text(l.hotspotStop),
                  ),
                ],
              ),
            ),
          );
        },
      ),
    );
  }
}

/// One numbered step of the way without a camera.
class _Step extends StatelessWidget {
  const _Step({required this.n, required this.title, this.child});

  final int n;
  final String title;
  final Widget? child;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final accent = Theme.of(context).colorScheme.primary;
    return Container(
      margin: const EdgeInsets.only(bottom: 10),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(color: c.surface, border: Border.all(color: c.border), borderRadius: BorderRadius.circular(18)),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          CircleAvatar(
            radius: 14,
            backgroundColor: accent,
            child: Text('$n', style: const TextStyle(color: Colors.white, fontWeight: FontWeight.w800)),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w700, height: 1.4)),
                ?child,
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// A label and its value, the value selectable.
class _Field extends StatelessWidget {
  const _Field({required this.label, required this.value, this.valueKey});

  final String label;
  final String value;
  final Key? valueKey;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        children: [
          SizedBox(width: 110, child: Text(label, style: TextStyle(color: context.colors.muted))),
          Expanded(child: SelectableText(value, key: valueKey, style: const TextStyle(fontWeight: FontWeight.w700))),
        ],
      ),
    );
  }
}
