# Стадия 1: фронтенд. web/dist в .gitignore, значит его неоткуда взять,
# кроме как собрать здесь.
FROM node:22-alpine AS web

WORKDIR /web

# Манифесты копируются отдельно от исходников: слой с npm ci переиспользуется,
# пока не менялись зависимости. Правка .vue его не инвалидирует.
COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN npm run build


# Стадия 2: бэкенд.
FROM golang:1.26-alpine AS build

WORKDIR /src

# Тот же приём: модули скачиваются один раз и переживают правки в *.go.
COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./

# CGO_ENABLED=0 даёт статический бинарник: финальный образ на alpine с musl,
# а тулчейн Go линкуется с glibc, поэтому динамический бинарник там не
# запустился бы. Побочно Go переходит на собственный DNS-резолвер вместо
# системного getaddrinfo.
# -trimpath убирает абсолютные пути сборки из бинарника; -s -w выбрасывают
# таблицу символов и DWARF (~30% размера). Стектрейсы при панике остаются
# читаемыми: имена функций Go хранит отдельно от DWARF, в pclntab.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/messenger .


# Стадия 3: то, что поедет на инстанс. Ни node, ни тулчейна Go здесь нет.
FROM alpine:3.22

# Корневые сертификаты нужны autocert: обмен с Let's Encrypt по ACME идёт по HTTPS.
RUN apk add --no-cache ca-certificates \
 && adduser -D -H -u 10001 app

WORKDIR /app

# server.go ищет статику по ОТНОСИТЕЛЬНОМУ пути web/dist, поэтому раскладка
# внутри образа обязана повторять раскладку репозитория относительно WORKDIR.
COPY --from=build /out/messenger ./messenger
COPY --from=web   /web/dist      ./web/dist

# Процесс работает от непривилегированного пользователя, поэтому слушает 8080,
# а не 80: порты ниже 1024 требуют root или CAP_NET_BIND_SERVICE. Наружу
# 80 и 443 пробрасывает docker, это дело compose, а не образа.
USER app
EXPOSE 8080

ENTRYPOINT ["/app/messenger"]
