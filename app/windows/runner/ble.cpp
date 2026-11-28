#include "ble.h"

#include <windows.h>
#include <shellapi.h>

#include <algorithm>
#include <array>
#include <chrono>
#include <cstdio>
#include <set>

#include <winrt/Windows.Devices.Radios.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.Storage.Streams.h>

using namespace winrt;
using namespace Windows::Devices::Bluetooth;
using namespace Windows::Devices::Bluetooth::Advertisement;
using namespace Windows::Devices::Bluetooth::GenericAttributeProfile;
using namespace Windows::Devices::Radios;
using namespace Windows::Storage::Streams;

namespace {

// Bible §6.6, BLE-02: medXfer's service and its one characteristic (as in Ble.kt).
const guid kService{0xc46b12c3, 0x1dee, 0x482d, {0xa4, 0x90, 0xb1, 0x85, 0x21, 0x23, 0x12, 0xfb}};
const guid kMessage{0xc46b12c4, 0x1dee, 0x482d, {0xa4, 0x90, 0xb1, 0x85, 0x21, 0x23, 0x12, 0xfb}};
// The call characteristic (J8): a phone that holds its link with this computer subscribes to it.
const guid kCall{0xc46b12c5, 0x1dee, 0x482d, {0xa4, 0x90, 0xb1, 0x85, 0x21, 0x23, 0x12, 0xfb}};
// The service's UUID as it goes over the air (little-endian), in a service data section.
constexpr std::array<uint8_t, 16> kServiceLe{0xfb, 0x12, 0x23, 0x21, 0x85, 0xb1, 0x90, 0xa4, 0x2d, 0x48, 0xee, 0x1d, 0xc3, 0x12, 0x6b, 0xc4};
constexpr uint8_t kHolds = 2;  // a phone's scan response: it connects to the computers it finds (Ble.kt)
constexpr size_t kMaxMessage = 512;                      // BLE-03
constexpr uint8_t kBleVersion = 1;                        // its messages' first byte (bible §6.6)
constexpr auto kAnswerWithin = std::chrono::seconds(25);  // BLE-11: under the 30 s of a GATT transaction
constexpr int64_t kSeenEveryMs = 2000;  // "seen" at most this often per address

std::vector<uint8_t> ToBytes(IBuffer const& b) {
  std::vector<uint8_t> v(b ? b.Length() : 0);
  if (!v.empty()) DataReader::FromBuffer(b).ReadBytes(v);
  return v;
}

IBuffer ToBuffer(const std::vector<uint8_t>& v, size_t from = 0) {
  DataWriter w;
  if (from < v.size()) w.WriteBytes(array_view<uint8_t const>(v.data() + from, v.data() + v.size()));
  return w.DetachBuffer();
}

std::string Address(uint64_t a) {
  char t[24];
  std::snprintf(t, sizeof(t), "%012llx", static_cast<unsigned long long>(a));
  return t;
}

int64_t NowMs() {
  return std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::steady_clock::now().time_since_epoch()).count();
}

flutter::EncodableValue V(const std::string& s) { return flutter::EncodableValue(s); }

// Hidden (WP-D.0): not discoverable, so no computer name in the advertisement and a random
// address that changes; the UUID alone (BLE-02), and one byte of service data, the same on every
// computer (the BLE version): without it, the Galaxy A57's scan filtered on the service never sees
// this computer, which a TECNO does (3 November 2026: 0 results in 15 s without it, 11 and 12
// with it).
GattServiceProviderAdvertisingParameters AdvertisingParameters() {
  GattServiceProviderAdvertisingParameters params;
  params.IsConnectable(true);
  params.IsDiscoverable(false);
  DataWriter version;
  version.WriteByte(kBleVersion);
  params.ServiceData(version.DetachBuffer());
  return params;
}

Windows::Foundation::IAsyncOperation<Radio> BluetoothRadio() {
  auto adapter = co_await BluetoothAdapter::GetDefaultAsync();
  if (!adapter) co_return nullptr;
  co_return co_await adapter.GetRadioAsync();
}

}  // namespace

// A phone's link kept with this computer (J8), by which this one calls it.
struct Ble::Held {
  GattSubscribedClient client{nullptr};
  std::vector<uint8_t> outgoing;  // this computer's message, which the phone reads
  std::vector<uint8_t> writing;   // the phone's answer, in pieces
  Ble::Answer done;               // the call under way
  uint64_t number = 0;            // its number, for its time limit
};

// A read that came before the core's answer: answered by Reply, or failed after kAnswerWithin.
struct Ble::Read {
  GattReadRequest request{nullptr};
  Windows::Foundation::Deferral deferral{nullptr};
  uint32_t offset = 0;
  bool answered = false;
};

// A device connected to this one's service.
struct Ble::Served {
  std::vector<uint8_t> writing;  // the message written, in pieces for a long write
  bool written = false;          // a message written and not yet given to the core
  std::optional<std::vector<uint8_t>> reply;
  std::vector<std::shared_ptr<Read>> waiting;
  GattSession session{nullptr};
  event_token status{};
};

// This computer connected to another device's service: one exchange at a time.
// Its members under mu_: Disconnect closes them while an exchange may be running, which works
// on copies of them.
struct Ble::Client {
  BluetoothLEDevice device{nullptr};
  GattSession session{nullptr};
  GattCharacteristic characteristic{nullptr};
  bool busy = false;
  bool closed = false;  // what the exchange running opens next is closed at once

  void Close() {
    closed = true;
    try {
      if (session) session.Close();
      if (device) device.Close();
    } catch (...) {
    }
    session = nullptr;
    device = nullptr;
    characteristic = nullptr;
  }
};

Ble::Ble(Emit emit) : emit_(std::move(emit)) {}

Ble::~Ble() {
  {
    std::lock_guard<std::mutex> lock(gate_->mu);
    gate_->alive.store(false);
  }
  Stop();
  provider_ = nullptr;
}

fire_and_forget Ble::Enabled(std::function<void(bool on)> done) {
  apartment_context ui;
  auto gate = gate_;
  bool on = false;
  try {
    if (Radio radio = co_await BluetoothRadio()) on = radio.State() == RadioState::On;
  } catch (...) {
  }
  co_await ui;
  if (gate->alive.load()) done(on);
}

fire_and_forget Ble::TurnOn(std::function<void(bool on)> done) {
  apartment_context ui;
  auto gate = gate_;
  bool on = false;
  try {
    if (co_await Radio::RequestAccessAsync() == RadioAccessStatus::Allowed) {
      if (Radio radio = co_await BluetoothRadio()) {
        on = radio.State() == RadioState::On || co_await radio.SetStateAsync(RadioState::On) == RadioAccessStatus::Allowed;
      }
    }
  } catch (...) {
  }
  co_await ui;
  if (!gate->alive.load()) co_return;
  // Not allowed to: the user turns it on in Windows' settings.
  if (!on) ShellExecuteW(nullptr, L"open", L"ms-settings:bluetooth", nullptr, nullptr, SW_SHOWNORMAL);
  done(on);
}

fire_and_forget Ble::Start(bool advertise, bool scan, Done done) {
  apartment_context ui;
  auto gate = gate_;
  {
    std::lock_guard<std::mutex> lock(gate->mu);
    gate->ui = ui;
  }
  std::optional<std::string> error;
  try {
    auto adapter = co_await BluetoothAdapter::GetDefaultAsync();
    Radio radio = adapter ? co_await adapter.GetRadioAsync() : nullptr;
    co_await ui;
    if (!gate->alive.load()) co_return;
    if (!adapter || !adapter.IsLowEnergySupported()) {
      error = "unsupported";
    } else if (!radio || radio.State() != RadioState::On) {
      error = "bluetooth_off";
    } else if (advertise && !provider_ && !adapter.IsPeripheralRoleSupported()) {
      error = "no_advertiser";
    }
    // The service kept from the last start: its advertisement again. A new service would make
    // Windows tell the paired devices their copy of this computer's services is stale, which
    // held the link with the Galaxy A57 30 s (real test of 7 November).
    if (!error && advertise && provider_ && !advertising_) {
      try {
        provider_.StartAdvertising(AdvertisingParameters());
        advertising_ = true;
      } catch (...) {
        provider_ = nullptr;  // lost with the radio: made again below
      }
    }
    if (!error && advertise && !provider_) {
      auto created = co_await GattServiceProvider::CreateAsync(kService);
      co_await ui;
      if (!gate->alive.load()) co_return;
      if (created.Error() != BluetoothError::Success) {
        error = "no_gatt_server";
      } else {
        auto provider = created.ServiceProvider();
        GattLocalCharacteristicParameters p;
        p.CharacteristicProperties(GattCharacteristicProperties::Write | GattCharacteristicProperties::Read);
        p.WriteProtectionLevel(GattProtectionLevel::Plain);
        p.ReadProtectionLevel(GattProtectionLevel::Plain);
        auto made = co_await provider.Service().CreateCharacteristicAsync(kMessage, p);
        co_await ui;
        if (!gate->alive.load()) co_return;
        if (made.Error() != BluetoothError::Success) {
          error = "no_gatt_server";
        } else {
          auto characteristic = made.Characteristic();
          characteristic.WriteRequested([this, gate](auto&&, GattWriteRequestedEventArgs const& args) {
            if (gate->alive.load()) OnWrite(args, gate);
          });
          characteristic.ReadRequested([this, gate](auto&&, GattReadRequestedEventArgs const& args) {
            if (gate->alive.load()) OnRead(args, gate);
          });
          provider.AdvertisementStatusChanged([this, gate](auto&&, GattServiceProviderAdvertisementStatusChangedEventArgs const& e) {
            if (gate->alive.load() && e.Status() == GattServiceProviderAdvertisementStatus::Aborted) {
              EmitOnUi("advertise_failed", {{V("code"), flutter::EncodableValue(static_cast<int32_t>(e.Error()))}}, gate);
            }
          });
          // The call characteristic: read and written by the phone that holds its link, and
          // notified by this computer to call it (J8).
          GattLocalCharacteristicParameters c;
          c.CharacteristicProperties(GattCharacteristicProperties::Write | GattCharacteristicProperties::Read |
                                     GattCharacteristicProperties::Notify);
          c.WriteProtectionLevel(GattProtectionLevel::Plain);
          c.ReadProtectionLevel(GattProtectionLevel::Plain);
          auto callMade = co_await provider.Service().CreateCharacteristicAsync(kCall, c);
          co_await ui;
          if (!gate->alive.load()) co_return;
          if (callMade.Error() == BluetoothError::Success) {
            call_ = callMade.Characteristic();
            call_.WriteRequested([this, gate](auto&&, GattWriteRequestedEventArgs const& args) {
              if (gate->alive.load()) OnCallWrite(args, gate);
            });
            call_.ReadRequested([this, gate](auto&&, GattReadRequestedEventArgs const& args) {
              if (gate->alive.load()) OnCallRead(args, gate);
            });
            call_.SubscribedClientsChanged([this, gate](auto&&, auto&&) {
              if (gate->alive.load()) OnSubscribers(gate);
            });
          }
          provider.StartAdvertising(AdvertisingParameters());
          provider_ = provider;
          advertising_ = true;
        }
      }
    }
    if (!error && scan && !watcher_) {
      BluetoothLEAdvertisementWatcher watcher;
      watcher.ScanningMode(BluetoothLEScanningMode::Active);
      // Not filtered by Windows: a phone's scan response, which says it holds (J8), carries the
      // service in its data, not in a list of UUIDs.
      watcher.Received([this, gate](auto&&, BluetoothLEAdvertisementReceivedEventArgs const& a) {
        if (!gate->alive.load()) return;
        bool ours = false, holds = false;
        auto adv = a.Advertisement();
        for (auto const& u : adv.ServiceUuids()) ours = ours || u == kService;
        for (auto const& section : adv.DataSections()) {
          if (section.DataType() != 0x21 || section.Data().Length() < kServiceLe.size()) continue;
          auto b = ToBytes(section.Data());
          if (!std::equal(kServiceLe.begin(), kServiceLe.end(), b.begin())) continue;
          ours = true;
          holds = holds || (b.size() > kServiceLe.size() && b[kServiceLe.size()] == kHolds);
        }
        if (ours) OnSeen(a.BluetoothAddress(), a.RawSignalStrengthInDBm(), static_cast<int>(a.BluetoothAddressType()), holds, gate);
      });
      watcher.Stopped([this, gate](auto&&, BluetoothLEAdvertisementWatcherStoppedEventArgs const& e) {
        if (gate->alive.load() && e.Error() != BluetoothError::Success) {
          EmitOnUi("scan_failed", {{V("code"), flutter::EncodableValue(static_cast<int32_t>(e.Error()))}}, gate);
        }
      });
      watcher.Start();
      watcher_ = watcher;
    }
  } catch (hresult_error const& e) {
    char t[16];
    std::snprintf(t, sizeof(t), "%08x", static_cast<unsigned>(e.code()));
    error = std::string("error_") + t;
  }
  co_await ui;
  if (gate->alive.load()) done(error);
}

void Ble::Stop() {
  if (provider_ && advertising_) {
    try {
      provider_.StopAdvertising();
    } catch (...) {
    }
  }
  advertising_ = false;
  if (watcher_) {
    try {
      watcher_.Stop();
    } catch (...) {
    }
    watcher_ = nullptr;
  }
  auto gate = gate_;
  std::vector<Answer> failed;
  {
    std::lock_guard<std::mutex> lock(gate->mu);
    for (auto& [conn, s] : served_) {
      for (auto& r : s->waiting) {
        if (r->answered) continue;
        r->answered = true;
        try {
          r->request.RespondWithProtocolError(GattProtocolError::UnlikelyError());
        } catch (...) {
        }
        r->deferral.Complete();
      }
      if (s->session) s->session.SessionStatusChanged(s->status);
    }
    served_.clear();
    seen_at_.clear();
    address_type_.clear();
    holds_.clear();
    for (auto& [conn, h] : held_) {
      if (h->done) failed.push_back(std::move(h->done));
    }
    held_.clear();
    for (auto& [device, c] : clients_) c->Close();
    clients_.clear();
  }
  for (auto& f : failed) AnswerOnUi(std::move(f), "stopped", {}, gate);
}

fire_and_forget Ble::EmitOnUi(std::string event, flutter::EncodableMap args, std::shared_ptr<Gate> gate) {
  std::optional<apartment_context> ui;
  {
    std::lock_guard<std::mutex> lock(gate->mu);
    if (!gate->alive.load() || !gate->ui) co_return;
    ui = gate->ui;
  }
  co_await *ui;
  if (gate->alive.load()) emit_(event, std::move(args));
}

fire_and_forget Ble::OnSeen(uint64_t address, int16_t rssi, int type, bool holds, std::shared_ptr<Gate> gate) {
  bool said;
  {
    std::lock_guard<std::mutex> lock(gate->mu);
    if (!gate->alive.load()) co_return;
    address_type_[address] = type;
    auto& known = holds_[address];
    bool news = holds && !known;  // said at once: this computer waits for that phone's link
    known = known || holds;
    said = known;
    auto now = NowMs();
    auto it = seen_at_.find(address);
    if (!news && it != seen_at_.end() && now - it->second < kSeenEveryMs) co_return;
    seen_at_[address] = now;
  }
  EmitOnUi("seen",
           {{V("device"), V(Address(address))},
            {V("rssi"), flutter::EncodableValue(static_cast<int32_t>(rssi))},
            {V("holds"), flutter::EncodableValue(said)}},
           gate);
  co_return;
}

fire_and_forget Ble::AnswerOnUi(Answer done, std::optional<std::string> error, std::vector<uint8_t> answer,
                                std::shared_ptr<Gate> gate) {
  if (!done) co_return;
  std::optional<apartment_context> ui;
  {
    std::lock_guard<std::mutex> lock(gate->mu);
    if (!gate->alive.load() || !gate->ui) co_return;
    ui = gate->ui;
  }
  co_await *ui;
  if (gate->alive.load()) done(error, std::move(answer));
}

// A phone's link gone; mu_ held. Its call under way fails.
void Ble::DropHeldLocked(const std::string& conn, std::vector<Answer>& failed, bool& dropped) {
  auto it = held_.find(conn);
  dropped = it != held_.end();
  if (!dropped) return;
  if (it->second->done) failed.push_back(std::move(it->second->done));
  held_.erase(it);
}

// The phones subscribed to the call characteristic, their link up: each one new is a held link,
// said to Dart; one gone, its link released. Run at each change, and at each message a phone
// writes: a paired phone's subscription is kept by Windows from one link to the next, and its
// new link changes nothing Windows would tell (real test of 18 November 2026).
void Ble::OnSubscribers(std::shared_ptr<Gate> gate) {
  std::vector<std::pair<std::string, bool>> changes;
  std::vector<Answer> failed;
  {
    std::lock_guard<std::mutex> lock(gate->mu);
    if (!gate->alive.load() || !call_) return;
    std::set<std::string> now;
    for (auto const& c : call_.SubscribedClients()) {
      GattSession session = c.Session();
      if (session.SessionStatus() != GattSessionStatus::Active) continue;
      auto conn = to_string(session.DeviceId().Id());
      now.insert(conn);
      if (held_.count(conn)) continue;
      auto h = std::make_shared<Held>();
      h->client = c;
      held_[conn] = h;
      ServedLocked(session);  // its closing said
      changes.emplace_back(conn, true);
    }
    std::vector<std::string> gone;
    for (auto& [conn, h] : held_) {
      if (!now.count(conn)) gone.push_back(conn);
    }
    for (auto& conn : gone) {
      bool dropped = false;
      DropHeldLocked(conn, failed, dropped);
      if (dropped) changes.emplace_back(conn, false);
    }
  }
  for (auto& [conn, on] : changes) {
    std::fprintf(stderr, "ble: %s %s\n", on ? "held" : "released", conn.c_str());
    EmitOnUi("held", {{V("conn"), V(conn)}, {V("held"), flutter::EncodableValue(on)}}, gate);
  }
  for (auto& f : failed) AnswerOnUi(std::move(f), "closed", {}, gate);
}

// Calls the phone over its link: [message] is what it reads next on the call characteristic,
// its answer what it writes there; the notification tells it to.
fire_and_forget Ble::Call(std::shared_ptr<Held> h, std::vector<uint8_t> message, Answer done, std::shared_ptr<Gate> gate) {
  uint64_t number;
  GattSubscribedClient client{nullptr};
  GattLocalCharacteristic call_char{nullptr};
  Answer superseded;
  {
    std::lock_guard<std::mutex> lock(gate->mu);
    if (!gate->alive.load() || !call_) co_return;
    // A call left unanswered (its notification sent before the phone listened, real test of
    // 18 November 2026) gives way to this one, rather than hold it for kAnswerWithin.
    if (h->done) superseded = std::move(h->done);
    h->outgoing = std::move(message);
    h->writing.clear();
    h->done = std::move(done);
    number = ++h->number;
    client = h->client;
    call_char = call_;
  }
  if (superseded) AnswerOnUi(std::move(superseded), "superseded", {}, gate);
  bool notified = false;
  try {
    DataWriter signal;
    signal.WriteByte(kBleVersion);
    auto result = co_await call_char.NotifyValueAsync(signal.DetachBuffer(), client);
    notified = result.Status() == GattCommunicationStatus::Success;
  } catch (...) {
  }
  if (!notified) {
    Answer failed;
    {
      std::lock_guard<std::mutex> lock(gate->mu);
      if (!gate->alive.load()) co_return;
      if (h->number == number) failed = std::move(h->done);
    }
    AnswerOnUi(std::move(failed), "notify_failed", {}, gate);
    co_return;
  }
  co_await resume_after(kAnswerWithin);
  Answer late;
  {
    std::lock_guard<std::mutex> lock(gate->mu);
    if (!gate->alive.load()) co_return;
    if (h->number == number && h->done) late = std::move(h->done);
  }
  AnswerOnUi(std::move(late), "timeout", {}, gate);
}

// The phone writes its answer to this computer's call, in pieces for a long write.
fire_and_forget Ble::OnCallWrite(GattWriteRequestedEventArgs args, std::shared_ptr<Gate> gate) {
  auto deferral = args.GetDeferral();
  auto request = co_await args.GetRequestAsync();
  if (!request) {
    deferral.Complete();
    co_return;
  }
  auto conn = to_string(args.Session().DeviceId().Id());
  auto value = ToBytes(request.Value());
  bool ok = false;
  bool alive = false;
  {
    std::lock_guard<std::mutex> lock(gate->mu);
    alive = gate->alive.load();
    if (alive) {
      auto it = held_.find(conn);
      if (it != held_.end() && it->second->done) {
        auto& h = *it->second;
        if (request.Offset() == 0) h.writing.clear();
        ok = request.Offset() == h.writing.size() && h.writing.size() + value.size() <= kMaxMessage;
        if (ok) h.writing.insert(h.writing.end(), value.begin(), value.end());
      }
    }
  }
  if (!alive) {
    deferral.Complete();
    co_return;
  }
  try {
    if (!ok) {
      request.RespondWithProtocolError(GattProtocolError::InvalidOffset());
    } else if (request.Option() == GattWriteOption::WriteWithResponse) {
      request.Respond();
    }
  } catch (...) {
  }
  deferral.Complete();
}

// The phone reads the call characteristic: this computer's message; once it wrote its answer,
// the read that marks the end of it, which completes the call.
fire_and_forget Ble::OnCallRead(GattReadRequestedEventArgs args, std::shared_ptr<Gate> gate) {
  auto deferral = args.GetDeferral();
  auto request = co_await args.GetRequestAsync();
  if (!request) {
    deferral.Complete();
    co_return;
  }
  auto conn = to_string(args.Session().DeviceId().Id());
  std::vector<uint8_t> value;
  Answer finished;
  std::vector<uint8_t> answer;
  bool known = false;
  bool alive = false;
  {
    std::lock_guard<std::mutex> lock(gate->mu);
    alive = gate->alive.load();
    if (alive) {
      auto it = held_.find(conn);
      if (it != held_.end()) {
        known = true;
        auto& h = *it->second;
        if (request.Offset() == 0 && !h.writing.empty()) {
          finished = std::move(h.done);
          answer = std::move(h.writing);
          h.writing.clear();
          h.outgoing.clear();
        } else {
          value = h.outgoing;
        }
      }
    }
  }
  if (!alive) {
    deferral.Complete();
    co_return;
  }
  try {
    if (!known) {
      request.RespondWithProtocolError(GattProtocolError::UnlikelyError());
    } else {
      request.RespondWithValue(ToBuffer(value, request.Offset()));
    }
  } catch (...) {
  }
  deferral.Complete();
  AnswerOnUi(std::move(finished), std::nullopt, std::move(answer), gate);
}

// The state of the device connected by session; gate_->mu held. Its going is said ("closed").
Ble::Served& Ble::ServedLocked(GattSession const& session) {
  auto conn = to_string(session.DeviceId().Id());
  auto& s = served_[conn];
  if (!s) {
    s = std::make_shared<Served>();
    s->session = session;
    auto gate = gate_;
    s->status = session.SessionStatusChanged([this, gate, conn](GattSession const&, GattSessionStatusChangedEventArgs const& e) {
      if (e.Status() != GattSessionStatus::Closed) return;
      std::vector<Answer> failed;
      bool dropped = false;
      {
        std::lock_guard<std::mutex> lock(gate->mu);
        if (!gate->alive.load()) return;
        auto it = served_.find(conn);
        if (it == served_.end()) return;
        for (auto& r : it->second->waiting) {
          if (r->answered) continue;
          r->answered = true;
          r->deferral.Complete();
        }
        served_.erase(it);
        DropHeldLocked(conn, failed, dropped);
      }
      if (dropped) EmitOnUi("held", {{V("conn"), V(conn)}, {V("held"), flutter::EncodableValue(false)}}, gate);
      for (auto& f : failed) AnswerOnUi(std::move(f), "closed", {}, gate);
      EmitOnUi("closed", {{V("conn"), V(conn)}}, gate);
    });
  }
  return *s;
}

// A write from a device connected to this one: kept, in pieces for a long write, until it reads
// the answer, which marks the end of the message.
fire_and_forget Ble::OnWrite(GattWriteRequestedEventArgs args, std::shared_ptr<Gate> gate) {
  auto deferral = args.GetDeferral();
  auto request = co_await args.GetRequestAsync();
  if (!request) {
    deferral.Complete();
    co_return;
  }
  auto value = ToBytes(request.Value());
  bool ok = true;
  bool alive = false;
  {
    std::lock_guard<std::mutex> lock(gate->mu);
    alive = gate->alive.load();
    if (alive) {
      auto& s = ServedLocked(args.Session());
      if (request.Offset() == 0) {
        s.writing.clear();
        s.reply.reset();
      }
      if (request.Offset() != s.writing.size() || s.writing.size() + value.size() > kMaxMessage) {
        ok = false;
        s.writing.clear();
      } else {
        s.writing.insert(s.writing.end(), value.begin(), value.end());
        s.written = true;
      }
    }
  }
  if (!alive) {
    deferral.Complete();
    co_return;
  }
  try {
    if (!ok) {
      request.RespondWithProtocolError(GattProtocolError::InvalidOffset());
    } else if (request.Option() == GattWriteOption::WriteWithResponse) {
      request.Respond();
    }
  } catch (...) {
  }
  deferral.Complete();
  OnSubscribers(gate);
}

// A read: the core's answer when it is there; else the message written goes to the core, and the
// read waits for its answer (Reply), up to kAnswerWithin.
fire_and_forget Ble::OnRead(GattReadRequestedEventArgs args, std::shared_ptr<Gate> gate) {
  auto deferral = args.GetDeferral();
  auto request = co_await args.GetRequestAsync();
  if (!request) {
    deferral.Complete();
    co_return;
  }
  OnSubscribers(gate);  // its subscription, made after its message (Ble.kt), seen by now
  auto conn = to_string(args.Session().DeviceId().Id());
  auto read = std::make_shared<Read>();
  read->request = request;
  read->deferral = deferral;
  read->offset = request.Offset();
  std::optional<std::vector<uint8_t>> reply;
  std::optional<std::vector<uint8_t>> message;
  bool alive = false;
  {
    std::lock_guard<std::mutex> lock(gate->mu);
    alive = gate->alive.load();
    if (alive) {
      auto& s = ServedLocked(args.Session());
      if (s.reply) {
        reply = s.reply;
      } else {
        s.waiting.push_back(read);
        if (s.written) {
          message = s.writing;
          s.written = false;
        }
      }
    }
  }
  if (!alive) {
    deferral.Complete();
    co_return;
  }
  if (reply) {
    try {
      request.RespondWithValue(ToBuffer(*reply, read->offset));
    } catch (...) {
    }
    deferral.Complete();
    co_return;
  }
  if (message) EmitOnUi("message", {{V("conn"), V(conn)}, {V("data"), flutter::EncodableValue(*message)}}, gate);
  co_await resume_after(kAnswerWithin);
  std::lock_guard<std::mutex> lock(gate->mu);
  if (!gate->alive.load() || read->answered) co_return;
  read->answered = true;
  try {
    request.RespondWithProtocolError(GattProtocolError::UnlikelyError());
  } catch (...) {
  }
  deferral.Complete();
}

void Ble::Reply(const std::string& conn, std::vector<uint8_t> data) {
  std::lock_guard<std::mutex> lock(gate_->mu);
  auto it = served_.find(conn);
  if (it == served_.end()) return;
  auto& s = *it->second;
  s.reply = data;
  for (auto& r : s.waiting) {
    if (r->answered) continue;
    r->answered = true;
    try {
      r->request.RespondWithValue(ToBuffer(data, r->offset));
    } catch (...) {
    }
    r->deferral.Complete();
  }
  s.waiting.clear();
}

fire_and_forget Ble::Exchange(std::string device, std::vector<uint8_t> message, Answer done) {
  apartment_context ui;
  auto gate = gate_;
  // A phone's held link (J8): called, never connected to.
  std::shared_ptr<Held> held;
  {
    std::lock_guard<std::mutex> lock(gate->mu);
    if (!gate->ui) gate->ui = ui;
    auto it = held_.find(device);
    if (it != held_.end()) held = it->second;
  }
  if (held) {
    Call(held, std::move(message), std::move(done), gate);
    co_return;
  }
  std::shared_ptr<Client> c;
  {
    std::lock_guard<std::mutex> lock(gate->mu);
    auto& slot = clients_[device];
    if (!slot || slot->closed) slot = std::make_shared<Client>();
    c = slot;
    if (c->busy) {
      done("busy", {});
      co_return;
    }
    c->busy = true;
  }
  std::optional<std::string> error;
  std::vector<uint8_t> answer;
  co_await resume_background();
  if (!gate->alive.load()) co_return;
  const char* step = "device";  // where an exception came from
  // Kept in c for Disconnect to close; false once it did, the object then closed here.
  auto keep = [gate, &c](auto&& set) {
    std::lock_guard<std::mutex> lock(gate->mu);
    if (!gate->alive.load() || c->closed) return false;
    set(*c);
    return true;
  };
  GattCharacteristic ch{nullptr};
  uint64_t address = 0;
  bool valid_address = false;
  // Held links ("BluetoothLE#...") never use GATT client addresses: if dropped concurrently,
  // stoull would throw std::invalid_argument outside try and invoke std::terminate().
  if (device.find('#') == std::string::npos) {
    try {
      size_t parsed = 0;
      address = std::stoull(device, &parsed, 16);
      valid_address = !device.empty() && parsed == device.size();
    } catch (const std::exception&) {
      valid_address = false;
    }
  }
  int type = -1;  // not seen: Windows guesses
  if (!valid_address) {
    error = device.find('#') != std::string::npos ? "disconnected" : "invalid_device";
  } else {
    std::lock_guard<std::mutex> lock(gate->mu);
    if (!gate->alive.load()) co_return;
    ch = c->characteristic;
    auto t = address_type_.find(address);
    if (t != address_type_.end()) type = t->second;
  }
  // Each step's time, said on stderr when this exchange connects: on 17 November 2026 the
  // computer's recognitions of the phone hung 6 s, four in a row, then one took 0.9 s.
  const int64_t began = NowMs();
  int64_t mark = began;
  std::string steps;
  auto lap = [&](const char* name) {
    auto now = NowMs();
    steps += std::string(" ") + name + " " + std::to_string(now - mark);
    mark = now;
  };
  bool connecting = valid_address && !ch;
  try {
    if (!error && !ch) {
      // As advertised: without its type, Windows takes the address for a public one unless it
      // still knows it, and a phone's is random.
      BluetoothLEDevice dev = type >= 0 ? co_await BluetoothLEDevice::FromBluetoothAddressAsync(address, static_cast<BluetoothAddressType>(type))
                                        : co_await BluetoothLEDevice::FromBluetoothAddressAsync(address);
      lap("device");
      if (!dev) {
        error = "not_found";
      } else if (!keep([&](Client& k) { k.device = dev; })) {
        dev.Close();
        error = "closed";
      } else {
        step = "session";
        GattSession session = co_await GattSession::FromDeviceIdAsync(dev.BluetoothDeviceId());
        lap("session");
        if (!keep([&](Client& k) { k.session = session; })) {
          session.Close();
          throw hresult_error(RO_E_CLOSED);
        }
        session.MaintainConnection(true);
        // A full discovery first: asking for one service straight away failed against the
        // Galaxy A57 (WP-D.0). A phone paired in Bluetooth with this computer fails here, with
        // E_UNEXPECTED or "unreachable", where the same phone unpaired answers (real test of
        // 3 November 2026).
        step = "services";
        auto all = co_await dev.GetGattServicesAsync(BluetoothCacheMode::Uncached);
        lap("services");
        auto services = co_await dev.GetGattServicesForUuidAsync(kService, BluetoothCacheMode::Uncached);
        lap("service");
        if (services.Status() != GattCommunicationStatus::Success || services.Services().Size() == 0) {
          error = "no_service:" + std::to_string(static_cast<int>(all.Status())) + "/" +
                  std::to_string(all.Status() == GattCommunicationStatus::Success ? all.Services().Size() : 0) + "/" +
                  std::to_string(static_cast<int>(services.Status()));
        } else {
          step = "characteristic";
          auto found = co_await services.Services().GetAt(0).GetCharacteristicsForUuidAsync(kMessage, BluetoothCacheMode::Uncached);
          lap("characteristic");
          if (found.Status() != GattCommunicationStatus::Success || found.Characteristics().Size() == 0) {
            error = "no_characteristic:" + std::to_string(static_cast<int>(found.Status()));
          } else {
            ch = found.Characteristics().GetAt(0);
            if (!keep([&](Client& k) { k.characteristic = ch; })) throw hresult_error(RO_E_CLOSED);
          }
        }
      }
    }
    // Longer than the MTU, Windows writes and reads in pieces (long write, long read).
    if (!error) {
      step = "write";
      auto written = co_await ch.WriteValueWithResultAsync(ToBuffer(message), GattWriteOption::WriteWithResponse);
      if (written.Status() != GattCommunicationStatus::Success) {
        error = "write_status:" + std::to_string(static_cast<int>(written.Status()));
      } else {
        step = "read";
        auto read = co_await ch.ReadValueAsync(BluetoothCacheMode::Uncached);
        if (read.Status() != GattCommunicationStatus::Success) {
          error = "read_status:" + std::to_string(static_cast<int>(read.Status()));
        } else {
          answer = ToBytes(read.Value());
        }
      }
    }
  } catch (hresult_error const& e) {
    char t[16];
    std::snprintf(t, sizeof(t), "%08x", static_cast<unsigned>(e.code()));
    error = std::string(step) + "_" + t;
  } catch (...) {
    error = std::string(step) + "_error";
  }
  {
    std::lock_guard<std::mutex> lock(gate->mu);
    if (error || !gate->alive.load()) c->Close();  // the next exchange connects again
    c->busy = false;
  }
  if (connecting) {
    lap("exchange");
    std::fprintf(stderr, "ble: connect %s (%s address)%s, %lld ms%s%s\n", device.c_str(),
                 type == 1 ? "random" : type == 0 ? "public" : "unknown", steps.c_str(), static_cast<long long>(NowMs() - began),
                 error ? ", " : "", error ? error->c_str() : "");
    fflush(stderr);
  }
  co_await ui;
  if (gate->alive.load()) done(error, std::move(answer));
}

void Ble::Disconnect(const std::string& device) {
  std::shared_ptr<Client> c;
  {
    std::lock_guard<std::mutex> lock(gate_->mu);
    auto it = clients_.find(device);
    if (it == clients_.end()) return;
    c = it->second;
    clients_.erase(it);
    c->Close();
  }
}
