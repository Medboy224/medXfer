/// The devices found by BLE in the lists of devices nearby (WP-D.5 of the J6d plan), on the
/// phone's Connect screen and in the computer's connection column: the same list as the devices
/// of the same network, the path said. Touching one runs the exchange of [BleLink]: the network
/// created or joined, then the session (mutual trust) or the PIN the other shows (unknown).
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../connection/ble_link.dart';
import '../../connection/connection.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import 'dialogs.dart';

/// The devices found by BLE that the list does not show already: a device on the same network
/// is reached there, by its name (the alias of a trusted one, else medXfer-xxxx, DISC-07).
List<BleDevice> bleOnly(BleLink link, Connection connection) {
  final named = {for (final d in connection.nearby) d.name, if (connection.session case final s?) s.name};
  return [
    for (final d in link.devices)
      if (!named.contains(d.name)) d,
  ];
}

/// What the row of [d] says: where the exchange stands when it is the one touched, else the path
/// and whether it asks for a PIN.
String bleDetail(AppLocalizations l, BleLink link, BleDevice d) {
  if (link.target == d) {
    final stage = switch (link.stage) {
      BleStage.asking => l.bleStageAsking,
      BleStage.pinNeeded || BleStage.confirming => l.bleStageConfirming,
      BleStage.networking => l.bleStageNetworking,
      BleStage.connecting => l.bleStageConnecting,
      BleStage.idle || BleStage.failed => null,
    };
    if (stage != null) return stage;
  }
  return [l.pathBluetooth, if (d.weak) l.bleWeak, d.trusted ? l.nearbyTrusted : l.nearbyAsksPin].join(' · ');
}

/// One exchange at a time.
bool bleBusy(BleLink link) => link.stage != BleStage.idle && link.stage != BleStage.failed;

/// Why BLE does not run, with what to do: the permission, with its reason first (BLE-12), or
/// Bluetooth to turn on. Nothing when it runs, or where there is no BLE (D4).
class BleStatus extends StatelessWidget {
  const BleStatus({super.key, required this.link});

  final BleLink link;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final problem = link.problem;
    if (link.running || !link.supported || problem == null || problem == 'unsupported') return const SizedBox.shrink();
    final (text, action, onAction) = switch (problem) {
      'permission' => (l.bleWhy, l.bleAllow, link.start),
      'bluetooth_off' => (l.bleOff, l.bleTurnOn, link.turnOn),
      _ => (l.bleUnavailable(problem), null, null),
    };
    return Padding(
      padding: const EdgeInsets.fromLTRB(4, 0, 4, 10),
      child: Row(
        children: [
          Icon(Icons.bluetooth_disabled_rounded, color: c.muted, size: 20),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              text,
              key: const Key('ble-status'),
              style: TextStyle(color: c.muted, fontSize: 13, height: 1.35),
            ),
          ),
          if (action != null && onAction != null) TextButton(key: const Key('ble-action'), onPressed: () => unawaited(onAction()), child: Text(action)),
        ],
      ),
    );
  }
}

/// Follows the exchange this device started: asks for the PIN the other shows, and says why it
/// failed. Around the screen that lists the devices.
class BleFlow extends StatefulWidget {
  const BleFlow({super.key, required this.link, required this.child});

  final BleLink link;
  final Widget child;

  @override
  State<BleFlow> createState() => _BleFlowState();
}

class _BleFlowState extends State<BleFlow> {
  bool _asking = false;

  @override
  void initState() {
    super.initState();
    widget.link.addListener(_changed);
  }

  @override
  void dispose() {
    widget.link.removeListener(_changed);
    super.dispose();
  }

  void _changed() {
    if (!mounted) return;
    final link = widget.link;
    final d = link.target;
    if (d == null) return;
    if (link.stage == BleStage.pinNeeded && !_asking) unawaited(_askPin(d));
    if (link.stage == BleStage.failed) {
      final l = AppLocalizations.of(context);
      final why = switch (link.failure) {
        'refused' => l.bleFailedRefused(d.name),
        'no_network' => l.bleFailedNetwork(d.name),
        'no_band' => l.bleFailedNoWifi(d.name),
        'join' => l.bleFailedJoin(d.name),
        'far' => l.bleFailedFar(d.name),
        _ => l.bleFailedRadio(d.name),
      };
      // Said once: the exchange may start again.
      link.reset();
      ScaffoldMessenger.of(context)
        ..clearSnackBars()
        ..showSnackBar(SnackBar(content: Text(why)));
    }
  }

  Future<void> _askPin(BleDevice d) async {
    _asking = true;
    final answer = await askPin(context, target: d.device, device: d.name);
    _asking = false;
    if (answer == null) {
      widget.link.reset();
      return;
    }
    await widget.link.typePin(answer.$2);
  }

  @override
  Widget build(BuildContext context) => widget.child;
}
