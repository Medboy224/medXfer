/// Windows' firewall (brought forward from J8 after the real test of 1 November): a PC whose
/// firewall let medXfer in on its cable only (« Public »), its Wi-Fi being « Private », could
/// neither be reached nor send, and nothing said why. The app reads, without administrator
/// rights, the networks it is kept out of, and asks the user's permission (UAC) to be let in.
/// The native side is app/windows/runner/firewall.cpp. Elsewhere, nothing to check.
library;

import 'dart:io';

import 'package:flutter/services.dart';

/// A network this computer is on, and whether the firewall lets medXfer in on it.
class FirewallNetwork {
  const FirewallNetwork({required this.name, required this.category, required this.allowed});

  factory FirewallNetwork.fromMap(Map<Object?, Object?> m) => FirewallNetwork(
        name: (m['name'] as String?) ?? '',
        category: (m['category'] as String?) ?? 'public',
        allowed: m['allowed'] != false,
      );

  final String name;

  /// "public", "private" or "domain": the kind of network, as Windows classes it.
  final String category;
  final bool allowed;
}

abstract interface class Firewall {
  /// The networks this computer is on, each with whether medXfer is let in.
  Future<List<FirewallNetwork>> status();

  /// Asks the user to let medXfer in on every kind of network. False: refused, or failed.
  Future<bool> allow();
}

class NoFirewall implements Firewall {
  const NoFirewall();

  @override
  Future<List<FirewallNetwork>> status() async => const [];

  @override
  Future<bool> allow() async => false;
}

class WindowsFirewall implements Firewall {
  const WindowsFirewall();

  static const _channel = MethodChannel('dev.medxfer.app/firewall');

  @override
  Future<List<FirewallNetwork>> status() async {
    final list = await _channel.invokeListMethod<Object?>('status') ?? const [];
    return [
      for (final n in list)
        if (n is Map) FirewallNetwork.fromMap(n),
    ];
  }

  @override
  Future<bool> allow() async => await _channel.invokeMethod<bool>('allow') ?? false;
}

Firewall systemFirewall() => Platform.isWindows ? const WindowsFirewall() : const NoFirewall();
