package main

import (
"context"
"encoding/json"
"fmt"
"net/http"
"os"
"os/signal"
"syscall"
"time"

"github.com/confluentinc/confluent-kafka-go/v2/kafka"
"github.com/percipia/eslgo"
"github.com/prometheus/client_golang/prometheus"
"github.com/prometheus/client_golang/prometheus/promhttp"
"go.uber.org/zap"
)

var (
activeCalls = prometheus.NewGauge(prometheus.GaugeOpts{
Name: "sip_gateway_active_calls",
Help: "Number of currently active SIP calls",
})
callsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
Name: "sip_gateway_calls_total",
Help: "Total SIP calls by direction and hangup cause",
}, []string{"direction", "hangup_cause"})
eventPublishErrors = prometheus.NewCounter(prometheus.CounterOpts{
Name: "sip_gateway_kafka_publish_errors_total",
Help: "Kafka publish failures",
})
)

func init() {
prometheus.MustRegister(activeCalls, callsTotal, eventPublishErrors)
}

type CallEvent struct {
EventType   string            `json:"event_type"`
CallID      string            `json:"call_id"`
CallerID    string            `json:"caller_id"`
Destination string            `json:"destination"`
Direction   string            `json:"direction"`
StartTime   time.Time         `json:"start_time"`
Timestamp   time.Time         `json:"timestamp"`
Headers     map[string]string `json:"headers,omitempty"`
}

type CDREvent struct {
CallID      string    `json:"call_id"`
CallerID    string    `json:"caller_id"`
Destination string    `json:"destination"`
StartTime   time.Time `json:"start_time"`
AnswerTime  time.Time `json:"answer_time,omitempty"`
EndTime     time.Time `json:"end_time"`
BillSeconds int       `json:"bill_seconds"`
HangupCause string    `json:"hangup_cause"`
Direction   string    `json:"direction"`
}

type KafkaPublisher struct {
producer  *kafka.Producer
callTopic string
cdrTopic  string
log       *zap.Logger
}

func NewKafkaPublisher(brokers, callTopic, cdrTopic string, log *zap.Logger) (*KafkaPublisher, error) {
p, err := kafka.NewProducer(&kafka.ConfigMap{
"bootstrap.servers":  brokers,
"acks":               "all",
"retries":            5,
"retry.backoff.ms":   100,
"enable.idempotence": true,
"compression.type":   "snappy",
"linger.ms":          5,
"batch.size":         65536,
})
if err != nil {
return nil, fmt.Errorf("kafka producer: %w", err)
}

go func() {
for e := range p.Events() {
if m, ok := e.(*kafka.Message); ok && m.TopicPartition.Error != nil {
log.Error("kafka delivery failed",
zap.String("topic", *m.TopicPartition.Topic),
zap.Error(m.TopicPartition.Error),
)
eventPublishErrors.Inc()
}
}
}()

return &KafkaPublisher{producer: p, callTopic: callTopic, cdrTopic: cdrTopic, log: log}, nil
}

func (kp *KafkaPublisher) PublishCallEvent(evt CallEvent) { kp.publish(kp.callTopic, evt.CallID, evt) }
func (kp *KafkaPublisher) PublishCDR(cdr CDREvent)        { kp.publish(kp.cdrTopic, cdr.CallID, cdr) }

func (kp *KafkaPublisher) publish(topic, key string, payload any) {
data, err := json.Marshal(payload)
if err != nil {
kp.log.Error("marshal error", zap.Error(err))
eventPublishErrors.Inc()
return
}
_ = kp.producer.Produce(&kafka.Message{
TopicPartition: kafka.TopicPartition{Topic: &topic, Partition: kafka.PartitionAny},
Key:            []byte(key),
Value:          data,
}, nil)
}

func (kp *KafkaPublisher) Close() { kp.producer.Flush(5000); kp.producer.Close() }

type ESLHandler struct {
conn *eslgo.Conn
pub  *KafkaPublisher
log  *zap.Logger
}

func (h *ESLHandler) handleChannelCreate(evt *eslgo.Event) {
callEvt := CallEvent{
EventType:   "CHANNEL_CREATE",
CallID:      evt.GetHeader("Unique-ID"),
CallerID:    evt.GetHeader("Caller-Caller-ID-Number"),
Destination: evt.GetHeader("Caller-Destination-Number"),
Direction:   evt.GetHeader("Call-Direction"),
StartTime:   time.Now(),
Timestamp:   time.Now(),
}
activeCalls.Inc()
h.pub.PublishCallEvent(callEvt)
h.log.Info("call created", zap.String("call_id", callEvt.CallID), zap.String("from", callEvt.CallerID))
}

func (h *ESLHandler) handleChannelAnswer(evt *eslgo.Event) {
h.pub.PublishCallEvent(CallEvent{
EventType:   "CHANNEL_ANSWER",
CallID:      evt.GetHeader("Unique-ID"),
CallerID:    evt.GetHeader("Caller-Caller-ID-Number"),
Destination: evt.GetHeader("Caller-Destination-Number"),
Direction:   evt.GetHeader("Call-Direction"),
Timestamp:   time.Now(),
})
}

func (h *ESLHandler) handleChannelDestroy(evt *eslgo.Event) {
callID := evt.GetHeader("Unique-ID")
billSec := 0
fmt.Sscanf(evt.GetHeader("variable_billsec"), "%d", &billSec)

cdr := CDREvent{
CallID:      callID,
CallerID:    evt.GetHeader("Caller-Caller-ID-Number"),
Destination: evt.GetHeader("Caller-Destination-Number"),
EndTime:     time.Now(),
BillSeconds: billSec,
HangupCause: evt.GetHeader("Hangup-Cause"),
Direction:   evt.GetHeader("Call-Direction"),
}
activeCalls.Dec()
callsTotal.WithLabelValues(cdr.Direction, cdr.HangupCause).Inc()
h.pub.PublishCDR(cdr)
h.log.Info("call ended", zap.String("call_id", callID), zap.Int("bill_sec", billSec))
}

func getEnv(key, fallback string) string {
if v := os.Getenv(key); v != "" {
return v
}
return fallback
}

func main() {
log, _ := zap.NewProduction()
defer log.Sync()

pub, err := NewKafkaPublisher(
getEnv("KAFKA_BROKERS", "kafka:9092"),
getEnv("KAFKA_TOPIC_CALL_EVENTS", "call-events"),
getEnv("KAFKA_TOPIC_CDR", "cdr-events"),
log,
)
if err != nil {
log.Fatal("kafka publisher", zap.Error(err))
}
defer pub.Close()

eslAddr := fmt.Sprintf("%s:%s", getEnv("FS_HOST", "127.0.0.1"), getEnv("FS_PORT", "8021"))
conn, err := eslgo.Dial(eslAddr, getEnv("FS_PASSWORD", "ClueCon"), func() {
log.Warn("FreeSWITCH ESL disconnected")
})
if err != nil {
log.Fatal("ESL connect", zap.String("addr", eslAddr), zap.Error(err))
}
defer conn.Close()

handler := &ESLHandler{conn: conn, pub: pub, log: log}
ctx := context.Background()

conn.RegisterEventListener(eslgo.EventListenAll, func(event *eslgo.Event) {
switch event.GetHeader("Event-Name") {
case "CHANNEL_CREATE":
handler.handleChannelCreate(event)
case "CHANNEL_ANSWER":
handler.handleChannelAnswer(event)
case "CHANNEL_DESTROY":
handler.handleChannelDestroy(event)
}
})

if err := conn.SendCommand(ctx, eslgo.Command{
UId: "",
App: "event plain CHANNEL_CREATE CHANNEL_ANSWER CHANNEL_DESTROY",
}); err != nil {
log.Fatal("ESL subscribe", zap.Error(err))
}

mux := http.NewServeMux()
mux.Handle("/metrics", promhttp.Handler())
mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
w.WriteHeader(http.StatusOK)
w.Write([]byte(`{"status":"ok"}`))
})
go http.ListenAndServe(":8080", mux)

log.Info("SIP event bridge running", zap.String("esl", eslAddr))
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
<-quit
log.Info("shutting down")
}
