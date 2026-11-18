// BLE probe, Windows side (WP-D.0 of the J6d plan). System APIs only (C++/WinRT).
//   ble-probe caps                      adapter capabilities
//   ble-probe serve [seconds] [bytes]   advertise the medXfer service, answer writes and reads
//   ble-probe scan [seconds]            list the devices that advertise the medXfer service
//   ble-probe exchange <addr> [bytes]   connect, write then read a message, with the delays
//   ble-probe publish                   which advertisement sections Windows lets an app send
//   ble-probe hold [rounds] [seconds]   the phone always connects and stays (J8): every [seconds],
//                                       a notification calls it over its own link; it writes the
//                                       token back, reads, writes and reads (BleProbe.holder)
//   ble-probe call [rounds] [seconds]   the phone always connects (J8): every [seconds], a fresh
//                                       token in this computer's advertisement calls the phone,
//                                       which connects, writes it back, then reads this
//                                       computer's message and writes its reply (BleProbe.callee)
// PROBE_HIDDEN=1: serve without being discoverable (no PC name in the advertisement).
// PROBE_TAG=1: serve with a second, non-connectable advertisement carrying 10 bytes.
// Its Android side is BleProbe.kt; the results are in docs/medxfer-plan-j6d.md. Built by build.bat.
#include <windows.h>
#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.Devices.Bluetooth.h>
#include <winrt/Windows.Devices.Bluetooth.Advertisement.h>
#include <winrt/Windows.Devices.Bluetooth.GenericAttributeProfile.h>
#include <winrt/Windows.Devices.Radios.h>
#include <winrt/Windows.Storage.Streams.h>

#include <chrono>
#include <condition_variable>
#include <cstdarg>
#include <random>
#include <cstdio>
#include <cstdlib>
#include <mutex>
#include <set>
#include <string>
#include <thread>

#include <winrt/Windows.Devices.Enumeration.h>
#include <vector>

using namespace winrt;
using namespace Windows::Devices::Bluetooth;
using namespace Windows::Devices::Bluetooth::Advertisement;
using namespace Windows::Devices::Bluetooth::GenericAttributeProfile;
using namespace Windows::Storage::Streams;

namespace {

const guid kService{0xc46b12c3, 0x1dee, 0x482d, {0xa4, 0x90, 0xb1, 0x85, 0x21, 0x23, 0x12, 0xfb}};
const guid kMessage{0xc46b12c4, 0x1dee, 0x482d, {0xa4, 0x90, 0xb1, 0x85, 0x21, 0x23, 0x12, 0xfb}};
// The call probe's own service: the medXfer app, open on the phone, leaves it alone.
const guid kCallService{0xc46b12d3, 0x1dee, 0x482d, {0xa4, 0x90, 0xb1, 0x85, 0x21, 0x23, 0x12, 0xfb}};
const guid kCallMessage{0xc46b12d4, 0x1dee, 0x482d, {0xa4, 0x90, 0xb1, 0x85, 0x21, 0x23, 0x12, 0xfb}};

using Clock = std::chrono::steady_clock;
const auto kStart = Clock::now();
std::mutex out;

double Ms(Clock::time_point t) { return std::chrono::duration<double, std::milli>(t - kStart).count(); }

void Log(const char* fmt, ...) {
  std::lock_guard<std::mutex> lock(out);
  std::printf("%9.1f ms  ", Ms(Clock::now()));
  va_list ap;
  va_start(ap, fmt);
  std::vprintf(fmt, ap);
  va_end(ap);
  std::printf("\n");
  std::fflush(stdout);
}

IBuffer Bytes(size_t n, uint8_t seed) {
  DataWriter w;
  for (size_t i = 0; i < n; i++) w.WriteByte(static_cast<uint8_t>(seed + i));
  return w.DetachBuffer();
}

std::string Hex(IBuffer const& b, size_t max = 12) {
  auto r = DataReader::FromBuffer(b);
  std::string s;
  char t[4];
  for (uint32_t i = 0; i < b.Length() && i < max; i++) {
    std::snprintf(t, sizeof(t), "%02x", r.ReadByte());
    s += t;
  }
  if (b.Length() > max) s += "..";
  return s;
}

std::string Addr(uint64_t a) {
  char t[24];
  std::snprintf(t, sizeof(t), "%012llx", static_cast<unsigned long long>(a));
  return t;
}

const char* Status(GattCommunicationStatus s) {
  switch (s) {
    case GattCommunicationStatus::Success: return "success";
    case GattCommunicationStatus::Unreachable: return "unreachable";
    case GattCommunicationStatus::ProtocolError: return "protocol error";
    case GattCommunicationStatus::AccessDenied: return "access denied";
  }
  return "?";
}

int Caps() {
  auto a = BluetoothAdapter::GetDefaultAsync().get();
  if (!a) { Log("no Bluetooth adapter"); return 1; }
  auto radio = a.GetRadioAsync().get();
  Log("address %s, LE %d, central %d, peripheral %d, offload %d, radio %s", Addr(a.BluetoothAddress()).c_str(),
      a.IsLowEnergySupported(), a.IsCentralRoleSupported(), a.IsPeripheralRoleSupported(), a.IsAdvertisementOffloadSupported(),
      radio && radio.State() == Windows::Devices::Radios::RadioState::On ? "on" : "off");
  try {
    Log("extended advertising %d, max advertisement %u bytes", a.IsExtendedAdvertisingSupported(), a.MaxAdvertisementDataLength());
  } catch (...) {
    Log("extended advertising: not reported by this Windows");
  }
  return 0;
}

int Serve(int seconds, size_t reply) {
  auto r = GattServiceProvider::CreateAsync(kService).get();
  if (r.Error() != BluetoothError::Success) { Log("service provider: error %d", static_cast<int>(r.Error())); return 1; }
  auto provider = r.ServiceProvider();
  GattLocalCharacteristicParameters p;
  p.CharacteristicProperties(GattCharacteristicProperties::Write | GattCharacteristicProperties::Read);
  p.WriteProtectionLevel(GattProtectionLevel::Plain);
  p.ReadProtectionLevel(GattProtectionLevel::Plain);
  auto cr = provider.Service().CreateCharacteristicAsync(kMessage, p).get();
  if (cr.Error() != BluetoothError::Success) { Log("characteristic: error %d", static_cast<int>(cr.Error())); return 1; }
  auto ch = cr.Characteristic();
  ch.WriteRequested([](GattLocalCharacteristic const&, GattWriteRequestedEventArgs const& args) {
    auto deferral = args.GetDeferral();
    auto req = args.GetRequestAsync().get();
    if (req) {
      Log("write from %ls: %u bytes at offset %u (%s)", args.Session().DeviceId().Id().c_str(), req.Value().Length(), req.Offset(),
          Hex(req.Value()).c_str());
      if (req.Option() == GattWriteOption::WriteWithResponse) req.Respond();
    }
    deferral.Complete();
  });
  ch.ReadRequested([reply](GattLocalCharacteristic const&, GattReadRequestedEventArgs const& args) {
    auto deferral = args.GetDeferral();
    auto req = args.GetRequestAsync().get();
    if (req) {
      Log("read at offset %u, max pdu %u", req.Offset(), args.Session().MaxPduSize());
      req.RespondWithValue(Bytes(reply, 0x80));
    }
    deferral.Complete();
  });
  provider.AdvertisementStatusChanged([](GattServiceProvider const&, GattServiceProviderAdvertisementStatusChangedEventArgs const& e) {
    Log("advertisement status %d, error %d", static_cast<int>(e.Status()), static_cast<int>(e.Error()));
  });
  GattServiceProviderAdvertisingParameters ap;
  ap.IsConnectable(true);
  ap.IsDiscoverable(std::getenv("PROBE_HIDDEN") == nullptr);
  // 10 bytes of service data: what is left in 31 once the flags and the 128-bit UUID are in.
  try {
    // PROBE_DATA=n: n bytes of service data (0: none).
    size_t n = std::getenv("PROBE_DATA") ? std::strtoul(std::getenv("PROBE_DATA"), nullptr, 10) : 10;
    if (n > 0) ap.ServiceData(Bytes(n, 0x10));
  } catch (hresult_error const& e) {
    Log("service data refused: %ls", e.message().c_str());
  }
  provider.StartAdvertising(ap);
  // A second advertisement beside it, with the 10 bytes the first one has no room for: does it
  // go out under the same address, so that a scanner can tie the two together?
  BluetoothLEAdvertisementPublisher tag;
  if (std::getenv("PROBE_TAG")) {
    DataWriter w;
    static const uint8_t ours_le[16] = {0xfb, 0x12, 0x23, 0x21, 0x85, 0xb1, 0x90, 0xa4, 0x2d, 0x48, 0xee, 0x1d, 0xc3, 0x12, 0x6b, 0xc4};
    for (auto b : ours_le) w.WriteByte(b);
    for (int i = 0; i < 10; i++) w.WriteByte(static_cast<uint8_t>(0x10 + i));
    tag.Advertisement().DataSections().Append(BluetoothLEAdvertisementDataSection(0x21, w.DetachBuffer()));
    tag.Start();
    std::this_thread::sleep_for(std::chrono::milliseconds(300));
    Log("tag advertisement status %d", static_cast<int>(tag.Status()));
  }
  Log("serving %d s, reply %zu bytes", seconds, reply);
  std::this_thread::sleep_for(std::chrono::seconds(seconds));
  provider.StopAdvertising();
  if (std::getenv("PROBE_TAG")) tag.Stop();
  return 0;
}

int Scan(int seconds) {
  BluetoothLEAdvertisementWatcher w;
  w.ScanningMode(BluetoothLEScanningMode::Active);
  std::set<uint64_t> all, ours;
  w.Received([&](BluetoothLEAdvertisementWatcher const&, BluetoothLEAdvertisementReceivedEventArgs const& a) {
    auto adv = a.Advertisement();
    bool medxfer = false;
    for (auto const& u : adv.ServiceUuids()) medxfer = medxfer || u == kService;
    std::string sections;
    for (auto const& s : adv.DataSections()) {
      char t[24];
      std::snprintf(t, sizeof(t), " %02x:%u", s.DataType(), s.Data().Length());
      sections += t;
      if (s.DataType() == 0x03 || s.DataType() == 0x21) sections += "=" + Hex(s.Data(), 26);
      if (s.DataType() == 0x21 && s.Data().Length() >= 16) {
        // The UUID goes over the air little-endian: the canonical text, byte by byte, reversed.
        static const uint8_t ours_le[16] = {0xfb, 0x12, 0x23, 0x21, 0x85, 0xb1, 0x90, 0xa4, 0x2d, 0x48, 0xee, 0x1d, 0xc3, 0x12, 0x6b, 0xc4};
        auto r = DataReader::FromBuffer(s.Data());
        bool same = true;
        for (int i = 0; i < 16; i++) same = r.ReadByte() == ours_le[i] && same;
        medxfer = medxfer || same;
      }
    }
    std::lock_guard<std::mutex> lock(out);
    all.insert(a.BluetoothAddress());
    if (!medxfer) return;
    bool first = ours.insert(a.BluetoothAddress()).second;
    std::printf("%9.1f ms  %s %s rssi %d type %d sections%s\n", Ms(Clock::now()), first ? "NEW " : "    ",
                Addr(a.BluetoothAddress()).c_str(), a.RawSignalStrengthInDBm(), static_cast<int>(a.AdvertisementType()),
                sections.c_str());
    std::fflush(stdout);
  });
  w.Stopped([](BluetoothLEAdvertisementWatcher const&, BluetoothLEAdvertisementWatcherStoppedEventArgs const& e) {
    Log("watcher stopped, error %d", static_cast<int>(e.Error()));
  });
  w.Start();
  Log("scanning %d s", seconds);
  std::this_thread::sleep_for(std::chrono::seconds(seconds));
  w.Stop();
  std::lock_guard<std::mutex> lock(out);
  std::printf("devices seen: %zu, medXfer: %zu\n", all.size(), ours.size());
  return 0;
}

int Exchange(uint64_t addr, size_t bytes) {
  auto t0 = Clock::now();
  auto dev = BluetoothLEDevice::FromBluetoothAddressAsync(addr).get();
  if (!dev) { Log("device %s not found", Addr(addr).c_str()); return 1; }
  auto t1 = Clock::now();
  Log("device found, connection status %d", static_cast<int>(dev.ConnectionStatus()));
  // Variants for a phone paired with this computer (#258): PROBE_NOSESSION, no GattSession;
  // PROBE_CACHED, the system's cache; PROBE_WAIT=ms before the discovery.
  GattSession session{nullptr};
  if (!std::getenv("PROBE_NOSESSION")) {
    session = GattSession::FromDeviceIdAsync(dev.BluetoothDeviceId()).get();
    Log("session opened");
    session.MaintainConnection(true);
  }
  if (auto wait = std::getenv("PROBE_WAIT")) std::this_thread::sleep_for(std::chrono::milliseconds(std::atoi(wait)));
  const auto mode = std::getenv("PROBE_CACHED") ? BluetoothCacheMode::Cached : BluetoothCacheMode::Uncached;
  Log("paired: %d", dev.DeviceInformation().Pairing().IsPaired() ? 1 : 0);
  auto all = dev.GetGattServicesAsync(mode).get();
  Log("all services: %s, %u", Status(all.Status()), all.Status() == GattCommunicationStatus::Success ? all.Services().Size() : 0);
  if (all.Status() == GattCommunicationStatus::Success)
    for (auto const& s : all.Services()) Log("  service %08x-...", s.Uuid().Data1);
  auto sr = dev.GetGattServicesForUuidAsync(kService, mode).get();
  auto t2 = Clock::now();
  if (sr.Status() != GattCommunicationStatus::Success || sr.Services().Size() == 0) {
    Log("service: %s, %u found", Status(sr.Status()), sr.Services().Size());
    return 1;
  }
  auto cr = sr.Services().GetAt(0).GetCharacteristicsForUuidAsync(kMessage, mode).get();
  if (cr.Status() != GattCommunicationStatus::Success || cr.Characteristics().Size() == 0) {
    Log("characteristic: %s", Status(cr.Status()));
    return 1;
  }
  auto ch = cr.Characteristics().GetAt(0);
  auto t3 = Clock::now();
  auto wr = ch.WriteValueWithResultAsync(Bytes(bytes, 0x01), GattWriteOption::WriteWithResponse).get();
  auto t4 = Clock::now();
  auto rd = ch.ReadValueAsync(BluetoothCacheMode::Uncached).get();
  auto t5 = Clock::now();
  auto ms = [](Clock::time_point a, Clock::time_point b) { return std::chrono::duration<double, std::milli>(b - a).count(); };
  Log("device %.0f ms, services %.0f ms, characteristic %.0f ms, max pdu %u", ms(t0, t1), ms(t1, t2), ms(t2, t3),
      session ? session.MaxPduSize() : 0);
  Log("write %zu bytes: %s in %.0f ms", bytes, Status(wr.Status()), ms(t3, t4));
  Log("read: %s, %u bytes in %.0f ms (%s)", Status(rd.Status()), rd.Status() == GattCommunicationStatus::Success ? rd.Value().Length() : 0,
      ms(t4, t5), rd.Status() == GattCommunicationStatus::Success ? Hex(rd.Value()).c_str() : "");
  Log("total %.0f ms", ms(t0, t5));
  if (session) session.Close();
  dev.Close();
  return 0;
}

int Publish() {
  struct Try { uint8_t type; size_t len; const char* what; };
  const Try tries[] = {
      {0x01, 1, "flags"},
      {0x07, 16, "complete list of 128-bit UUIDs"},
      {0x21, 26, "128-bit service data, 10 bytes"},
      {0x09, 8, "complete local name"},
      {0xff, 12, "manufacturer data"},
  };
  for (auto const& t : tries) {
    BluetoothLEAdvertisementPublisher p;
    try {
      p.Advertisement().DataSections().Append(BluetoothLEAdvertisementDataSection(t.type, Bytes(t.len, 0)));
      p.Start();
      std::this_thread::sleep_for(std::chrono::milliseconds(300));
      Log("%02x %-34s status %d", t.type, t.what, static_cast<int>(p.Status()));
      p.Stop();
    } catch (hresult_error const& e) {
      Log("%02x %-34s refused: %ls", t.type, t.what, e.message().c_str());
    }
  }
  return 0;
}

// The phone always connects (J8): this computer calls it by a fresh token in its advertisement.
// Each round: the token in the service data, then the time until the phone writes it back, the
// reads and writes of the exchange that follows, all on this computer's clock.
int Call(int rounds, int every) {

  std::mutex m;
  std::condition_variable cv;
  std::string token;  // the one being called, hex
  Clock::time_point calledAt;
  bool answered = false;
  int writes = 0, reads = 0;
  auto since = [&] { return std::chrono::duration<double, std::milli>(Clock::now() - calledAt).count(); };

  // The service, made again when PROBE_REMAKE drops it for the token: an Intel card advertises one
  // thing at a time, and StopAdvertising leaves the service's advertisement on (17 November).
  GattServiceProvider provider{nullptr};
  auto make = [&]() -> bool {
    auto r = GattServiceProvider::CreateAsync(kCallService).get();
    if (r.Error() != BluetoothError::Success) { Log("service provider: error %d", static_cast<int>(r.Error())); return false; }
    provider = r.ServiceProvider();
    GattLocalCharacteristicParameters p;
    p.CharacteristicProperties(GattCharacteristicProperties::Write | GattCharacteristicProperties::Read);
    p.WriteProtectionLevel(GattProtectionLevel::Plain);
    p.ReadProtectionLevel(GattProtectionLevel::Plain);
    auto cr = provider.Service().CreateCharacteristicAsync(kCallMessage, p).get();
    if (cr.Error() != BluetoothError::Success) { Log("characteristic: error %d", static_cast<int>(cr.Error())); return false; }
    auto ch = cr.Characteristic();
    ch.WriteRequested([&](GattLocalCharacteristic const&, GattWriteRequestedEventArgs const& args) {
      auto deferral = args.GetDeferral();
      auto req = args.GetRequestAsync().get();
      if (req) {
        auto v = req.Value();
        std::string hex = Hex(v, 8);
        std::lock_guard<std::mutex> lock(m);
        writes++;
        if (v.Length() == 5 && hex.rfind("ca", 0) == 0 && hex.substr(2) == token) {
          answered = true;
          Log("answered %s: %.0f ms after the call (%ls)", token.c_str(), since(), args.Session().DeviceId().Id().c_str());
          cv.notify_all();
        } else {
          Log("write %u bytes, %.0f ms after the call", v.Length(), since());
        }
        if (req.Option() == GattWriteOption::WriteWithResponse) req.Respond();
      }
      deferral.Complete();
    });
    ch.ReadRequested([&](GattLocalCharacteristic const&, GattReadRequestedEventArgs const& args) {
      auto deferral = args.GetDeferral();
      auto req = args.GetRequestAsync().get();
      if (req) {
        {
          std::lock_guard<std::mutex> lock(m);
          reads++;
          Log("read at offset %u, %.0f ms after the call", req.Offset(), since());
        }
        req.RespondWithValue(Bytes(200 - std::min<size_t>(200, req.Offset()), 0x80));
      }
      deferral.Complete();
    });
    provider.AdvertisementStatusChanged([&](GattServiceProvider const&, GattServiceProviderAdvertisementStatusChangedEventArgs const& e) {
      Log("advertisement status %d, error %d", static_cast<int>(e.Status()), static_cast<int>(e.Error()));
    });
    return true;
  };
  if (!make()) return 1;


  auto advertise = [&](std::vector<uint8_t> const& data) {
    GattServiceProviderAdvertisingParameters ap;
    ap.IsConnectable(true);
    ap.IsDiscoverable(false);
    DataWriter w;
    for (auto b : data) w.WriteByte(b);
    ap.ServiceData(w.DetachBuffer());
    provider.StartAdvertising(ap);
  };
  advertise({0x01});  // as the app: the messages' version, the same on every computer
  Log("advertising, %d rounds every %d s", rounds, every);

  std::mt19937 rng{std::random_device{}()};
  for (int i = 0; i < rounds; i++) {
    std::this_thread::sleep_for(std::chrono::seconds(every));
    std::vector<uint8_t> data{0x01, 0xca};
    char t[3];
    std::string hex;
    for (int k = 0; k < 4; k++) {
      uint8_t b = static_cast<uint8_t>(rng());
      data.push_back(b);
      std::snprintf(t, sizeof(t), "%02x", b);
      hex += t;
    }
    provider.StopAdvertising();
    if (std::getenv("PROBE_REMAKE")) provider = nullptr;  // the service gone with it
    {
      std::lock_guard<std::mutex> lock(m);
      token = hex;
      answered = false;
      calledAt = Clock::now();
    }
    // The token alone, in the call service's data: 2 + 16 + 5 = 23 bytes.
    static const uint8_t call_le[16] = {0xfb, 0x12, 0x23, 0x21, 0x85, 0xb1, 0x90, 0xa4, 0x2d, 0x48, 0xee, 0x1d, 0xd3, 0x12, 0x6b, 0xc4};
    // PROBE_GAP=ms: the service's advertisement given time to stop before the token's.
    if (auto gap = std::getenv("PROBE_GAP")) std::this_thread::sleep_for(std::chrono::milliseconds(std::atoi(gap)));
    if (provider) {
      auto t = Clock::now();
      while (provider.AdvertisementStatus() == GattServiceProviderAdvertisementStatus::Started ||
             provider.AdvertisementStatus() == GattServiceProviderAdvertisementStatus::StartedWithoutAllAdvertisementData) {
        if (Clock::now() - t > std::chrono::seconds(3)) break;
        std::this_thread::sleep_for(std::chrono::milliseconds(20));
      }
      Log("service advertisement %d after %.0f ms", static_cast<int>(provider.AdvertisementStatus()),
          std::chrono::duration<double, std::milli>(Clock::now() - t).count());
    }
    if (provider) Log("service advertisement status %d", static_cast<int>(provider.AdvertisementStatus()));
    BluetoothLEAdvertisementPublisher pub;
    pub.StatusChanged([](BluetoothLEAdvertisementPublisher const&, BluetoothLEAdvertisementPublisherStatusChangedEventArgs const& e) {
      Log("token publisher status %d, error %d", static_cast<int>(e.Status()), static_cast<int>(e.Error()));
    });
    DataWriter w;
    for (auto b : call_le) w.WriteByte(b);
    for (size_t k = 1; k < data.size(); k++) w.WriteByte(data[k]);
    pub.Advertisement().DataSections().Append(BluetoothLEAdvertisementDataSection(0x21, w.DetachBuffer()));
    pub.Start();
    Log("round %d: call %s", i + 1, hex.c_str());
    int hold = std::getenv("PROBE_HOLD") ? std::atoi(std::getenv("PROBE_HOLD")) : 600;
    std::this_thread::sleep_for(std::chrono::milliseconds(hold));
    Log("token advertisement status %d, stopped", static_cast<int>(pub.Status()));
    pub.Stop();
    if (!provider) {
      auto t = Clock::now();
      make();
      Log("service made again in %.0f ms", std::chrono::duration<double, std::milli>(Clock::now() - t).count());
    }
    advertise({0x01});
    std::unique_lock<std::mutex> lock(m);
    if (!cv.wait_for(lock, std::chrono::seconds(10), [&] { return answered; })) Log("round %d: no answer in 10 s", i + 1);
    lock.unlock();
    std::this_thread::sleep_for(std::chrono::seconds(3));  // the exchange that follows
  }
  provider.StopAdvertising();
  Log("writes %d, reads %d", writes, reads);
  return 0;
}

// The phone always connects, and keeps its link while both look for devices (J8): this computer
// calls it by a notification on that link, never by a request of its own (which a phone paired
// with it leaves unanswered 20 to 50 s), nor by a second advertisement (which an Intel card does
// not send while a GATT service exists, 18 November).
int Hold(int rounds, int every) {
  auto r = GattServiceProvider::CreateAsync(kCallService).get();
  if (r.Error() != BluetoothError::Success) { Log("service provider: error %d", static_cast<int>(r.Error())); return 1; }
  auto provider = r.ServiceProvider();
  GattLocalCharacteristicParameters p;
  p.CharacteristicProperties(GattCharacteristicProperties::Write | GattCharacteristicProperties::Read | GattCharacteristicProperties::Notify);
  p.WriteProtectionLevel(GattProtectionLevel::Plain);
  p.ReadProtectionLevel(GattProtectionLevel::Plain);
  auto cr = provider.Service().CreateCharacteristicAsync(kCallMessage, p).get();
  if (cr.Error() != BluetoothError::Success) { Log("characteristic: error %d", static_cast<int>(cr.Error())); return 1; }
  auto ch = cr.Characteristic();

  std::mutex m;
  std::condition_variable cv;
  std::string token;
  Clock::time_point calledAt = Clock::now();
  bool answered = false;
  auto since = [&] { return std::chrono::duration<double, std::milli>(Clock::now() - calledAt).count(); };
  ch.SubscribedClientsChanged([&](GattLocalCharacteristic const& c, auto&&) {
    Log("subscribed clients: %u", c.SubscribedClients().Size());
    std::lock_guard<std::mutex> lock(m);
    cv.notify_all();
  });
  ch.WriteRequested([&](GattLocalCharacteristic const&, GattWriteRequestedEventArgs const& args) {
    auto deferral = args.GetDeferral();
    auto req = args.GetRequestAsync().get();
    if (req) {
      auto v = req.Value();
      std::string hex = Hex(v, 8);
      std::lock_guard<std::mutex> lock(m);
      if (v.Length() == 5 && hex.rfind("ca", 0) == 0 && hex.substr(2) == token) {
        answered = true;
        Log("answered %s: %.0f ms after the notification", token.c_str(), since());
        cv.notify_all();
      } else {
        Log("write %u bytes, %.0f ms after the notification", v.Length(), since());
      }
      if (req.Option() == GattWriteOption::WriteWithResponse) req.Respond();
    }
    deferral.Complete();
  });
  ch.ReadRequested([&](GattLocalCharacteristic const&, GattReadRequestedEventArgs const& args) {
    auto deferral = args.GetDeferral();
    auto req = args.GetRequestAsync().get();
    if (req) {
      Log("read at offset %u, %.0f ms after the notification", req.Offset(), since());
      req.RespondWithValue(Bytes(200 - std::min<size_t>(200, req.Offset()), 0x80));
    }
    deferral.Complete();
  });
  GattServiceProviderAdvertisingParameters ap;
  ap.IsConnectable(true);
  ap.IsDiscoverable(false);
  {
    DataWriter w;
    w.WriteByte(0x01);
    ap.ServiceData(w.DetachBuffer());
  }
  provider.StartAdvertising(ap);
  Log("advertising; waiting for a phone to subscribe");
  {
    std::unique_lock<std::mutex> lock(m);
    if (!cv.wait_for(lock, std::chrono::seconds(60), [&] { return ch.SubscribedClients().Size() > 0; })) {
      Log("no phone subscribed in 60 s");
      return 1;
    }
  }
  std::mt19937 rng{std::random_device{}()};
  for (int i = 0; i < rounds; i++) {
    std::this_thread::sleep_for(std::chrono::seconds(every));
    DataWriter w;
    w.WriteByte(0xca);
    char t[3];
    std::string hex;
    for (int k = 0; k < 4; k++) {
      uint8_t b = static_cast<uint8_t>(rng());
      w.WriteByte(b);
      std::snprintf(t, sizeof(t), "%02x", b);
      hex += t;
    }
    {
      std::lock_guard<std::mutex> lock(m);
      token = hex;
      answered = false;
      calledAt = Clock::now();
    }
    auto results = ch.NotifyValueAsync(w.DetachBuffer()).get();
    std::string statuses;
    for (auto const& res : results) statuses += std::string(" ") + Status(res.Status());
    Log("round %d: notified %s to %u client(s):%s", i + 1, hex.c_str(), results.Size(), statuses.c_str());
    std::unique_lock<std::mutex> lock(m);
    if (!cv.wait_for(lock, std::chrono::seconds(10), [&] { return answered; })) Log("round %d: no answer in 10 s", i + 1);
    lock.unlock();
    std::this_thread::sleep_for(std::chrono::seconds(2));  // the exchange that follows
  }
  provider.StopAdvertising();
  return 0;
}

// The call's token alone, for [seconds], no GATT service beside it: does it go out at all?
int Beacon(int seconds) {
  static const uint8_t call_le[16] = {0xfb, 0x12, 0x23, 0x21, 0x85, 0xb1, 0x90, 0xa4, 0x2d, 0x48, 0xee, 0x1d, 0xd3, 0x12, 0x6b, 0xc4};
  BluetoothLEAdvertisementPublisher pub;
  pub.StatusChanged([](BluetoothLEAdvertisementPublisher const&, BluetoothLEAdvertisementPublisherStatusChangedEventArgs const& e) {
    Log("publisher status %d, error %d", static_cast<int>(e.Status()), static_cast<int>(e.Error()));
  });
  DataWriter w;
  for (auto b : call_le) w.WriteByte(b);
  for (uint8_t b : {0xca, 0x01, 0x02, 0x03, 0x04}) w.WriteByte(b);
  pub.Advertisement().DataSections().Append(BluetoothLEAdvertisementDataSection(0x21, w.DetachBuffer()));
  pub.Start();
  Log("beacon %d s", seconds);
  std::this_thread::sleep_for(std::chrono::seconds(seconds));
  pub.Stop();
  return 0;
}

}  // namespace

int main(int argc, char** argv) {
  init_apartment(apartment_type::multi_threaded);
  std::string cmd = argc > 1 ? argv[1] : "caps";
  try {
    if (cmd == "caps") return Caps();
    if (cmd == "serve") return Serve(argc > 2 ? std::atoi(argv[2]) : 60, argc > 3 ? std::strtoul(argv[3], nullptr, 10) : 200);
    if (cmd == "scan") return Scan(argc > 2 ? std::atoi(argv[2]) : 15);
    if (cmd == "exchange" && argc > 2)
      return Exchange(std::strtoull(argv[2], nullptr, 16), argc > 3 ? std::strtoul(argv[3], nullptr, 10) : 200);
    if (cmd == "publish") return Publish();
    if (cmd == "hold") return Hold(argc > 2 ? std::atoi(argv[2]) : 8, argc > 3 ? std::atoi(argv[3]) : 6);
    if (cmd == "beacon") return Beacon(argc > 2 ? std::atoi(argv[2]) : 20);
    if (cmd == "call") return Call(argc > 2 ? std::atoi(argv[2]) : 5, argc > 3 ? std::atoi(argv[3]) : 8);
  } catch (hresult_error const& e) {
    Log("error 0x%08x: %ls", static_cast<unsigned>(e.code()), e.message().c_str());
    return 1;
  }
  std::fprintf(stderr, "usage: ble-probe caps | serve [s] [bytes] | scan [s] | exchange <addr> [bytes] | publish\n");
  return 2;
}
