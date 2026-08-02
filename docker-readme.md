Оба контейнера запущены. PostgreSQL на порте 5432, pgAdmin на порте 5050.

### Доступ к pgAdmin:

URL: http://localhost:5050 <br>
Email: admin@local.dev <br>
Пароль: admin <br>

#### После входа в pgAdmin добавьте подключение к PostgreSQL: <br>

Host: postgres (имя контейнера в сети Docker) <br>
Port: 5432 <br>
Username: postgres <br>
Password: postgres <br>
Database: local_dev <br>

### Для подключения вашего локального приложения к БД:

Host: localhost <br>
Port: 5432 <br>
Username: postgres <br>
Password: postgres <br>
Database: local_dev <br>

### start/stop

Учетные данные находятся в .env файле — измените их на безопасные перед production. Данные БД сохраняются в Docker volume stady-go_postgres_data, поэтому они не теряются при перезагрузке контейнеров. Остановить сервисы: docker compose down. Запустить заново: docker compose up -d.

### postgres_data

 Папка postgres_data теперь находится локально в E:\GO\projects\stady-go\postgres_data — можешь видеть файлы PostgreSQL. Все данные будут сохраняться прямо на диск.
 
### connect

БД запущена и слушает на порту 5432. Проверь что ты используешь правильные учетные данные:

Для подключения:

*Host*: localhost (или 127.0.0.1) но скорее всего postgres <br> 
*Port*: 5432 <br>
**Username**: postgres <br>
**Password**: postgres <br>
**Database**: local_dev <br>