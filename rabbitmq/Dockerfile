# Documentation: https://hub.docker.com/_/rabbitmq/

FROM rabbitmq:3.7-management-alpine

 

RUN mkdir -p /data/mnesia && \

    mkdir -p /data/logs && \

    chown -R rabbitmq:rabbitmq /data

 

env RABBITMQ_MNESIA_BASE /data/mnesia

env RABBITMQ_LOG_BASE /data/logs

 

RUN rm -f /etc/rabbitmq/rabbitmq.conf

ADD rabbitmq.conf /etc/rabbitmq/rabbitmq.conf

 

EXPOSE 5672

EXPOSE 15672
