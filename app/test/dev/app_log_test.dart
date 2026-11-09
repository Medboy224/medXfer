import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/dev/app_log.dart';

void main() {
  test('the newest lines only, oldest first, each with its time', () {
    final log = AppLog(maxLines: 3);
    expect(log.text, '');
    for (final line in ['one', 'two', 'three', 'four']) {
      log.add(line);
    }
    final lines = log.text.split('\n');
    expect(lines.last, ''); // ends with a newline
    expect([for (final l in lines.take(3)) l.split(' ').last], ['two', 'three', 'four']);
    expect(DateTime.tryParse(lines.first.split(' ').first)?.isUtc, isTrue);
  });

  test('installed: every debugPrint, but the core\'s own log lines, which its journal keeps', () {
    final print = debugPrint;
    addTearDown(() => debugPrint = print);
    final printed = <String?>[];
    debugPrint = (String? message, {int? wrapWidth}) => printed.add(message);
    AppLog.install();
    debugPrint('ble: started, 70 ms');
    debugPrint('core log: {"msg":"listening"}');
    expect(printed, ['ble: started, 70 ms', 'core log: {"msg":"listening"}']); // still printed
    final text = AppLog.instance.text;
    expect(text, contains(' app: ')); // the system first
    expect(text, contains(' ble: started, 70 ms\n'));
    expect(text, isNot(contains('listening')));
  });
}
