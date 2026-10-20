/// Sizes and durations as the user reads them, in the user's language: "1,5 Mo" or "1.5 MB".
library;

import 'package:flutter/widgets.dart';

import '../l10n/app_localizations.dart';

String formatBytes(BuildContext context, num bytes) {
  final l = AppLocalizations.of(context);
  final comma = Localizations.localeOf(context).languageCode == 'fr';
  String n(double v) {
    final s = v >= 100 ? v.toStringAsFixed(0) : v.toStringAsFixed(1);
    final t = s.endsWith('.0') ? s.substring(0, s.length - 2) : s;
    return comma ? t.replaceAll('.', ',') : t;
  }

  const k = 1024.0;
  if (bytes < k) return l.unitB('${bytes.toInt()}');
  if (bytes < k * k) return l.unitKB(n(bytes / k));
  if (bytes < k * k * k) return l.unitMB(n(bytes / (k * k)));
  return l.unitGB(n(bytes / (k * k * k)));
}

String formatDuration(BuildContext context, int seconds) {
  final l = AppLocalizations.of(context);
  if (seconds < 60) return l.durationSeconds('$seconds');
  if (seconds < 3600) return l.durationMinutes('${seconds ~/ 60}', '${seconds % 60}');
  return l.durationHours('${seconds ~/ 3600}', '${(seconds % 3600) ~/ 60}');
}
