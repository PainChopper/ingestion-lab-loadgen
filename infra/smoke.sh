#!/usr/bin/env bash
set -Eeuo pipefail
export LC_ALL=C

fail() {
    printf 'Smoke failed: %s\n' "$*" >&2
    exit 1
}

for tool in docker curl jq kcat redis-cli psql timeout; do
    command -v "$tool" >/dev/null || fail "$tool is required"
done

infra_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
compose=(docker compose -f "$infra_dir/docker-compose.yml")
http=(curl --fail --silent --show-error --max-time 5 --noproxy '*')
proxy_api=http://127.0.0.1:8474/proxies
direct_status=http://127.0.0.1:8081/status
proxy_status=http://127.0.0.1:18081/status
topic_prefix="infra-smoke-$(date +%s)-$$-$RANDOM"
toxic="$topic_prefix-latency"
topics=()
toxic_pending=false

kafka() {
    local tool=$1
    shift
    "${compose[@]}" exec -T kafka timeout 40 "/opt/kafka/bin/$tool" "$@"
}

topic_admin() {
    kafka kafka-topics.sh --bootstrap-server kafka:29092 "$@"
}

remove_toxic() {
    local endpoint="$proxy_api/receiver-host/toxics/$toxic"
    local status
    status=$("${http[@]}" --no-fail --output /dev/null --write-out '%{http_code}' \
        --request DELETE "$endpoint") || return 1
    [[ $status == 204 || $status == 404 ]] || return 1
    toxic_pending=false
}

cleanup() {
    local result=$? topic remaining attempt
    trap - EXIT
    set +e
    if [[ $toxic_pending == true ]]; then
        remove_toxic || { printf 'Could not remove toxic %s\n' "$toxic" >&2; result=1; }
    fi
    for topic in "${topics[@]}"; do
        topic_admin --delete --if-exists --topic "$topic" >/dev/null || result=1
    done
    if (( ${#topics[@]} > 0 )); then
        # Kafka applies deletion asynchronously. Check the names from this run only.
        remaining=
        for attempt in {1..5}; do
            remaining=$(topic_admin --list) || { result=1; break; }
            if ! grep -q "^$topic_prefix-" <<< "$remaining"; then
                printf 'Removed smoke topics (%s)\n' "$topic_prefix"
                break
            fi
            if (( attempt == 5 )); then
                printf 'Smoke topics still exist (%s)\n' "$topic_prefix" >&2
                result=1
            else
                sleep 1
            fi
        done
    fi
    exit "$result"
}

check_proxies() {
    jq -e '
        def proxy($port; $upstream):
            (.listen | endswith(":" + $port)) and .upstream == $upstream
            and .enabled and .toxics == [];
        (.["kafka-host"] | proxy("19092"; "kafka:19092"))
        and (.["kafka-container"] | proxy("29093"; "kafka:29093"))
        and (.["receiver-host"] | proxy("18081"; "host.docker.internal:8081"))
    ' >/dev/null
}

check_kafka() {
    local route=$1 endpoint=$2 topic="$topic_prefix-$1" received metadata
    local payload="message-$topic" admin_endpoint=$endpoint
    # kcat handles the host metadata and data path. Topic administration uses the broker CLI.
    if [[ $route == host-* ]]; then
        admin_endpoint=kafka:29092
    fi
    topics+=("$topic")
    kafka kafka-topics.sh --bootstrap-server "$admin_endpoint" \
        --create --topic "$topic" --partitions 1 --replication-factor 1 >/dev/null

    case "$route" in
        host-*)
            local -a client=(timeout 30 kcat -q -F /dev/null -b "$endpoint" -m 5)
            "${client[@]}" -L -J -t "$topic" | jq -e --arg endpoint "$endpoint" '
                (.brokers | length) == 1
                and .brokers[0].name == $endpoint
                and (.topics[0].partitions[0] | .leader >= 0 and (.replicas | length) == 1)
            ' >/dev/null || fail "$route advertised the wrong broker or replication factor"
            printf '%s\n' "$payload" | "${client[@]}" -P -t "$topic" -p 0 -X acks=all
            received=$("${client[@]}" -C -t "$topic" -p 0 -o beginning -c 1 -f '%s')
            ;;
        *)
            metadata=$(kafka kafka-cluster.sh api-versions --bootstrap-server "$endpoint")
            grep -Fq "$endpoint (id:" <<< "$metadata" || fail "$route advertised the wrong broker"
            printf '%s\n' "$payload" | kafka kafka-console-producer.sh \
                --bootstrap-server "$endpoint" --topic "$topic" --command-property acks=all
            received=$(kafka kafka-console-consumer.sh --bootstrap-server "$endpoint" --topic "$topic" \
                --partition 0 --offset 0 --max-messages 1 --timeout-ms 10000)
            ;;
    esac

    [[ $received == "$payload" ]] || fail "$route did not return the message that was sent"
    printf 'Kafka %s (%s): advertised endpoint and RF1 round trip OK\n' "$route" "$endpoint"
}

host_database() (
    # Do not trace the password expansion, including when invoked with bash -x.
    set +x
    # shellcheck disable=SC2016
    password=$("${compose[@]}" exec -T postgres sh -c 'printf "%s." "$POSTGRES_PASSWORD"')
    PGPASSWORD=${password%.} PGCONNECT_TIMEOUT=5 psql -XqAtw \
        -h 127.0.0.1 -U ingestion_lab -d ingestion_lab -c 'SELECT 1'
)

receiver_id() {
    jq -er '.startedAt | select(type == "string" and length > 0)'
}

median_http_ms() {
    local url=$1 sample
    for _ in {1..3}; do
        sample=$("${http[@]}" --output /dev/null --write-out '%{http_code} %{time_total}' "$url") || return 1
        [[ $sample == "200 "* ]] || return 1
        printf '%s\n' "${sample#200 }"
    done | sort -n | awk 'NR == 2 { printf "%.0f\n", $1 * 1000 }'
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

"${compose[@]}" ps --format json | jq -es '
    [ .[] | if type == "array" then .[] else . end | select(.Health == "healthy") | .Service ]
    | sort == ["grafana", "kafka", "postgres", "prometheus", "redis", "toxiproxy"]
' >/dev/null || fail 'start the Compose stack with up --wait'
printf 'All six Compose services are healthy\n'

"${http[@]}" "$proxy_api" | check_proxies || fail 'expected three clean, enabled proxy routes'
printf 'Toxiproxy routes confirmed through the host API\n'

check_kafka host-direct 127.0.0.1:9092
check_kafka host-proxy 127.0.0.1:19092
check_kafka container-direct kafka:29092
check_kafka container-proxy toxiproxy:29093

redis_host=(timeout 10 redis-cli --raw -h 127.0.0.1)
[[ $("${redis_host[@]}" ping) == PONG ]] || fail 'host Redis PING'
[[ $("${redis_host[@]}" config get appendonly) == $'appendonly\nyes' ]] || fail 'host Redis appendonly'
[[ $("${redis_host[@]}" config get appendfsync) == $'appendfsync\neverysec' ]] || fail 'host Redis appendfsync'
[[ $("${compose[@]}" exec -T redis redis-cli --raw -h redis ping) == PONG ]] || fail 'container Redis PING'
printf 'Redis PING from host and Redis container, host appendonly=yes and appendfsync=everysec OK\n'

[[ $(host_database) == 1 ]] || fail 'host PostgreSQL ingestion_lab SELECT 1'
# Expand the password inside the container so it never appears in the command line.
# shellcheck disable=SC2016
database=$("${compose[@]}" exec -T postgres sh -c '
    export PGPASSWORD="$POSTGRES_PASSWORD" PGCONNECT_TIMEOUT=5
    exec psql -XqAtw -h postgres -U ingestion_lab -d ingestion_lab -c "SELECT 1"
')
[[ $database == 1 ]] || fail 'container PostgreSQL ingestion_lab SELECT 1'
printf 'PostgreSQL ingestion_lab SELECT 1 from host and PostgreSQL container OK\n'

receiver=$("${http[@]}" "$direct_status" | receiver_id) || fail 'start Receiver on host port 8081'
proxied_receiver=$("${http[@]}" "$proxy_status" | receiver_id)
container_receiver=$("${compose[@]}" exec -T kafka wget -qO- -T 5 \
    http://toxiproxy:18081/status | receiver_id)
[[ $receiver == "$proxied_receiver" && $receiver == "$container_receiver" ]] ||
    fail 'Toxiproxy did not reach the same host Receiver from both networks'
printf 'Host Receiver reached directly and through Toxiproxy from host and Compose\n'

baseline=$(median_http_ms "$proxy_status")
direct_before=$(median_http_ms "$direct_status")
toxic_pending=true
"${http[@]}" -H 'Content-Type: application/json' --data \
    "{\"name\":\"$toxic\",\"type\":\"latency\",\"stream\":\"downstream\",\"attributes\":{\"latency\":350,\"jitter\":0}}" \
    "$proxy_api/receiver-host/toxics" | jq -e --arg toxic "$toxic" '
        .name == $toxic and .type == "latency" and .stream == "downstream"
        and .attributes.latency == 350 and .attributes.jitter == 0
    ' >/dev/null
delayed=$(median_http_ms "$proxy_status")
direct_during=$(median_http_ms "$direct_status")
remove_toxic || fail "could not remove toxic $toxic"
restored=$(median_http_ms "$proxy_status")
"${http[@]}" "$proxy_api" | check_proxies
printf 'Receiver median latency (ms): proxy %s -> %s -> %s, direct %s -> %s\n' \
    "$baseline" "$delayed" "$restored" "$direct_before" "$direct_during"
(( delayed - baseline >= 250 && delayed - restored >= 250 && direct_during - direct_before < 150 )) ||
    fail 'latency did not increase and recover on the proxy route only'
