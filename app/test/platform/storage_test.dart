// The sorting of received files by type (decision J2): one folder per offer, by what it holds.
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/platform/storage.dart';

void main() {
  test('each file by its extension, whatever its case', () {
    expect(typeFolderOf('Vacances/IMG_0001.JPG'), 'Photos');
    expect(typeFolderOf(r'C:\clips\film.mkv'), 'Vidéos');
    expect(typeFolderOf('album/01 - titre.flac'), 'Musique');
    expect(typeFolderOf('jeu.apk'), 'Apps');
    expect(typeFolderOf('rapport.pdf'), 'Fichiers');
    expect(typeFolderOf('LISEZMOI'), 'Fichiers');
  });

  test('an offer stays whole: one type, its folder; a mix, Fichiers', () {
    expect(typeFolderFor(['Vacances/a.jpg', 'Vacances/b.heic']), 'Photos');
    expect(typeFolderFor(['Vacances/a.jpg', 'Vacances/clip.mp4']), 'Fichiers');
    expect(typeFolderFor(['jeu.apk']), 'Apps');
    expect(typeFolderFor([]), 'Fichiers');
  });
}
