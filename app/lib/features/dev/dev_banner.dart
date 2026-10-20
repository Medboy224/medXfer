/// The banner of the developer mode (DEV-13): a strip at the bottom of every screen while the
/// mode is on; a tap opens the developer panel.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../l10n/app_localizations.dart';
import '../../settings.dart';
import 'dev_panel.dart';

class DevBanner extends StatelessWidget {
  const DevBanner({super.key, required this.settings, required this.navigator, required this.child});

  final AppSettings settings;
  final GlobalKey<NavigatorState> navigator;
  final Widget child;

  /// The strip's colour: the same in every theme, apart from the accents.
  static const color = Color(0xFFB54708);

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: settings,
      child: child,
      builder: (context, child) {
        if (!settings.devMode) return child!;
        return Column(
          children: [
            // The screens above the strip: the system's bottom inset is the strip's.
            Expanded(child: MediaQuery.removePadding(context: context, removeBottom: true, child: child!)),
            Material(
              color: color,
              child: InkWell(
                key: const Key('dev-banner'),
                onTap: () => unawaited(navigator.currentState?.push(MaterialPageRoute<void>(builder: (_) => const DevPanel()))),
                child: SafeArea(
                  top: false,
                  child: SizedBox(
                    height: 24,
                    width: double.infinity,
                    child: Center(
                      child: Text(
                        AppLocalizations.of(context).devBanner,
                        style: const TextStyle(color: Colors.white, fontSize: 12, fontWeight: FontWeight.w700),
                      ),
                    ),
                  ),
                ),
              ),
            ),
          ],
        );
      },
    );
  }
}
