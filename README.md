##vk

Начнем с конфига и как собирать проект.
Данные для конфигурации проекта будут ледать в переменных окружения(файл .env ). 
Если чтение не проищоло ( не экспортировали переменные ), то ставяться базовые занчения уровня логгера(дефолтные логгер на уровне info) и порт 4040(можно поставить любой).

Логгер исползую от компании Uber , Zap - logger.

В этом проекте решил разместить прото файл и сгенерированные файлы в одной директории. Если это проект с большим количеством сервисов, то коненчо прото файлы надо хранить в отдлеьном метсе.

Собственно так, создаются файлы для сервера/сервиса. 
```cmd
protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative internal/proto/pubsub.proto
```
Важно отметить, что надо до этого установить protoc-gen-go, protoc-gen-go-grpc. Если не нравится protoc , то знаю что можно использовать "buf", но пока не использовал.

В Subpub находится требуемый по заданию интрефейс subpub, стурктура SubPubImplementation, которая и реализует этот итрефейс. 

В Server лежит grpcserver.
```go
type GrpcServer struct {
	pb.UnimplementedPubSubServer
	sp     subpub.SubPub
	logger *zap.Logger
}
```

В качестве проверки, использовал Postman.
Нужно сделать 2 вкладки с соединением, один будет публиковать (делать вызовы Publisher), а дургой принимать (вызвал Subscribe() и ждешь сообщения ). Статус коды есть.

Graceful shutdown реализуется вызовом 	grpcServer.GracefulStop(). Встроенная функция.
Dependency injection есть , но могло быть лучше , если в spLink указал на интерфейс, а не ссылку на структуру, тогда бы я вообще бы не зависил от это реализации.
Можно было бы использовать не in-memory хранилище, а любое другое.
