
FROM registry.access.redhat.com/ubi8 as builder
ARG GS_VERSION=2.20.2
ARG WAR_URL=https://downloads.sourceforge.net/project/geoserver/GeoServer/${GS_VERSION}/geoserver-${GS_VERSION}-war.zip

WORKDIR /tmp
RUN curl -L ${WAR_URL} -o /tmp/geoserver.zip && dnf -y install unzip && unzip /tmp/geoserver.zip -d /tmp


FROM registry.redhat.io/jboss-webserver-5/jws56-openjdk11-openshift-rhel8@sha256:ec10836f2ec107401e1586e7084e68615c1598c23e7564a6bed6dc2f37535255

COPY --from=builder /tmp/geoserver.war /opt/jws-5.6/tomcat/webapps/
#ADD deployments/geoserver.war /opt/jws-5.6/tomcat/webapps/
