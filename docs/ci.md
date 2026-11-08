# Проверки CI

Конфигурация — [`.github/workflows/ci.yml`](../.github/workflows/ci.yml). Это часть [PRD-31](https://linear.app/po-sostavu/issue/PRD-31): проверки PWA, Go API и контейнерной сборки. [CD](cd.md) работает в отдельном workflow. На VPS выполняется blue-green деплой проверенного коммита `main` по [регламенту релизов](configuration-and-releases.md#5-blue-green-развёртывание).

## Запуск и результат

Workflow `CI` запускается при открытии, обновлении и повторном открытии PR в `develop` или `main`, а также при push в эти ветки. Проверяются оба приложения, в том числе при изменении документации: фильтров по путям нет. Новый запуск для того же события и PR или ветки отменяет устаревший.

После публикации feature-ветки первый запуск выполняется через PR. Ручной запуск **Actions → CI → Run workflow** становится доступен после появления workflow в default branch репозитория. В PR нужно указать ссылку на завершившийся workflow и проверенный commit SHA.

`CI status` успешен, только если прошли проверки PWA, API, все сборки Docker и интеграционная проверка доставки. Ошибка, пропуск или отмена любого обязательного задания блокируют итоговый успех. Причина ошибки находится в логах соответствующего задания. После исправления нужно отправить новый коммит и дождаться его проверок.

## Что проверяется

| Задание              | Проверки                                                                                                 |
| -------------------- | -------------------------------------------------------------------------------------------------------- |
| PWA                  | `npm ci`, `npm run check`, `npm run build`. Аудит зависимостей                                           |
| API                  | `task api:fmt:check`, `task api:lint`, `task api:test:race`, `task api:build`, `task api:audit` из корня |
| Docker (pwa)         | Сборка PWA и Caddy с закреплёнными исправлениями зависимостей, target `runtime`                          |
| Docker (api)         | Сборка Go API, target `api`                                                                              |
| Docker (migrate)     | Сборка отдельного инструмента миграций, target `migrate`                                                 |
| Docker (postgres)    | Сборка PostgreSQL с исправлениями zlib и gosu, target `runtime`                                          |
| CI status            | Итог результатов всех заданий                                                                            |
| Delivery integration | Проверка артефактов, валидации выпуска, миграций, двух цветов и отката на изолированном стенде           |

`npm run check` включает Prettier, ESLint, Steiger, проверку типов TypeScript и существующие тесты Vitest. Go-тесты запускаются с race detector. Отдельный повтор обычного `go test` не нужен. Форматирование в CI только проверяется, файлы автоматически не исправляются.

Сборочный контекст PWA — корень репозитория, Dockerfile — `apps/pwa/Dockerfile`. API и миграции собираются из `apps/api`, цели — `api` и `migrate`. PostgreSQL собирается из `infra/postgres`, цель — `runtime`. Четыре сборки сохраняются через Actions Artifacts. Задание Delivery integration загружает эти же образы и запускает временный стенд с PostgreSQL и Traefik. CI не публикует образы в GHCR и не подключается к VPS. Содержимое базовых образов требует отдельного аудита.

Существующие тесты каркасов не проверяют ещё не реализованные продуктовые сценарии. Внешние OCR/ИИ API не вызываются. PostgreSQL интеграционного стенда содержит только тестовые данные. Проверки бизнес-инвариантов добавляются вместе с соответствующими методами API.

## Версии и безопасность

Задания выполняются на `ubuntu-24.04`. Node.js берётся из `apps/pwa/.nvmrc`, npm — из `packageManager` в `apps/pwa/package.json`, Go — из `apps/api/go.mod`. Task закреплён на `3.54.0`. Версии линтера и govulncheck берутся из `apps/api/tools/go.mod`. Кэш npm привязан к `package-lock.json`, кэш Go — к `go.sum` приложения и инструментов.

Actions закреплены полными commit SHA с комментариями версий. Токен workflow имеет только `contents: read`, checkout не сохраняет Git credentials. Рабочие секреты, данные пользователей и доступ к серверу не нужны. Используется `pull_request`, выполнение кода PR через `pull_request_target` не предусмотрено. У заданий ограничено время выполнения.

Политика аудита:

- `npm audit --omit=dev --audit-level=low` завершает проверку ошибкой при любой известной уязвимости production-зависимостей
- `npm audit --audit-level=moderate` проверяет полный граф, включая инструменты. Находки `low` видны в отчёте, `moderate`, `high` и `critical` завершают проверку ошибкой. Ошибки не скрываются через `continue-on-error`
- `task api:audit` запускает закреплённый govulncheck. Его ненулевой код завершения останавливает задание

Аудит зависит от доступности реестра npm и базы уязвимостей Go. Ошибка запроса не считается успешной проверкой. Нулевой результат относится только к проверенным уязвимостям на момент запуска и не гарантирует безопасность всего приложения. Исправления образов и оставшиеся ограничения описаны в [отчёте проверки контейнеров](container-security.md).

## Локальное воспроизведение

Из `apps/pwa`:

```sh
npm ci
npm run check
npm run build
npm audit --omit=dev --audit-level=low
npm audit --audit-level=moderate
```

Из корня, с установленными Go и Task. Для `-race` нужен C-компилятор:

```sh
task api:fmt:check
task api:lint
task api:test:race
task api:build
task api:audit
docker build -f apps/pwa/Dockerfile -t posostavu-pwa:ci .
docker build --target api -t posostavu-api:ci apps/api
docker build --target migrate -t posostavu-migrate:ci apps/api
docker build --target runtime -t posostavu-postgres:ci infra/postgres
```

## Слияние и дальнейшее подключение

Перед слиянием проверяется успешный статус `CI status` для актуального коммита. Локальные и ручные проверки, не покрытые workflow, по-прежнему указываются в PR.

Конфигурация workflow не изменяет branch protection и существующие права доступа. После первого подтверждённого запуска итоговый статус `CI status` можно отдельно включить в обязательные проверки ruleset для `develop` и `main`. Для веток `release/*` сначала нужно расширить события workflow. В текущей задаче правила GitHub не меняются.

CI не выпускает релиз и не выполняет деплой на VPS. Передача артефактов, публикация GHCR и отдельное включение выкладки описаны в [регламенте CD](cd.md).
